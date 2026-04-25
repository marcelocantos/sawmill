# Demo: transform_multi_root — Cross-Repo AST Transforms

This document demonstrates the `transform_multi_root` MCP tool end-to-end
across four project roots, including per-repo diff previews and graceful
error isolation when one root is unavailable.

## What the tool does

`transform_multi_root` accepts N≥1 absolute project-root paths and an
ordered list of transform specifications (same schema as `transform_batch`).
It loads (or reuses from the shared model pool) a `CodebaseModel` for each
root, applies the transforms, and returns a JSON object keyed by root path.
Each value is a `RootDiffBundle`:

```json
{
  "file_count": 2,
  "diffs": ["--- a/foo.py\n+++ b/foo.py\n..."],
  "error": ""          // non-empty only on per-root failure
}
```

Key properties:

- **No fixed upper bound on N.** The implementation iterates over the
  `roots` slice generically; adding more roots requires no code change.
- **Per-repo error isolation.** A root that fails to load (wrong path,
  permissions, corrupt database) records its error in `bundle.error` and
  the call continues with the remaining roots.
- **Session state untouched.** The tool never modifies the calling
  session's pending changes — it always works on isolated model instances.

---

## Scenario

We have four roots:

| Root | Contents | Expected outcome |
|------|----------|-----------------|
| `repo-alpha` | `alpha.py` with string `"hello"` | diff: `"hello"` → `"world"` |
| `repo-beta` | `beta.py`, `gamma.py` each with `"hello"` | two diffs |
| `repo-delta` | `delta.py` with `"hello"` | one diff |
| `/nonexistent/repo-epsilon` | does not exist | per-root error |

The transform replaces every string literal with `"world"` using the
Tree-sitter Python grammar.

---

## MCP call payload

```json
{
  "roots": [
    "/tmp/demo/repo-alpha",
    "/tmp/demo/repo-beta",
    "/tmp/demo/repo-delta",
    "/nonexistent/repo-epsilon"
  ],
  "transforms": [
    {
      "raw_query": "(string) @s",
      "capture": "s",
      "action": "replace",
      "code": "\"world\""
    }
  ]
}
```

---

## Expected output (abridged)

```
transform_multi_root: 4 root(s), 4 file(s) changed, 1 root(s) with errors

{
  "/tmp/demo/repo-alpha": {
    "file_count": 1,
    "diffs": [
      "--- a/alpha.py\n+++ b/alpha.py\n@@ -1 +1 @@\n-msg = \"hello\"\n+msg = \"world\"\n"
    ]
  },
  "/tmp/demo/repo-beta": {
    "file_count": 2,
    "diffs": [
      "--- a/beta.py\n+++ b/beta.py\n@@ -1 +1 @@\n-greeting = \"hello\"\n+greeting = \"world\"\n",
      "--- a/gamma.py\n+++ b/gamma.py\n@@ -1 +1 @@\n-farewell = \"bye\"\n+farewell = \"world\"\n"
    ]
  },
  "/tmp/demo/repo-delta": {
    "file_count": 1,
    "diffs": [
      "--- a/delta.py\n+++ b/delta.py\n@@ -1 +1 @@\n-constant = \"hello\"\n+constant = \"world\"\n"
    ]
  },
  "/nonexistent/repo-epsilon": {
    "file_count": 0,
    "error": "loading model: ..."
  }
}
```

Key observations:
- Three of four roots produce independent diffs.
- `repo-beta` returns two diffs (one per changed file).
- `/nonexistent/repo-epsilon` records an error without aborting the others.
- The summary line reports 4 root(s), 4 file(s) changed, and 1 root(s) with errors.

---

## Runnable Go test

The unit test `TestTransformMultiRootThreeRoots` in
`go/mcp/multi_root_test.go` exercises the same scenario programmatically
(N=3: two good roots with multi-file content + one bad root):

```bash
cd go
go test ./mcp/... -run TestTransformMultiRootThreeRoots -v -count=1
```

Expected output:

```
=== RUN   TestTransformMultiRootThreeRoots
--- PASS: TestTransformMultiRootThreeRoots (0.01s)
PASS
ok      github.com/marcelocantos/sawmill/mcp    0.6s
```

The test verifies:
1. All three roots appear in the result map.
2. `root1` has 1 file changed (1 diff).
3. `root2` has 2 files changed (2 diffs — `beta.py` and `gamma.py`).
4. `badRoot` has a non-empty `Error` field.
5. The summary line contains `3 root(s)`, `3 file(s)`, and `1 root(s) with errors`.

---

## Shell demo script

The script below creates four fixture directories, starts sawmill, calls
the MCP tool via HTTP JSON, and prints the result. It requires `sawmill
serve` to be running on `127.0.0.1:8765`.

```bash
#!/usr/bin/env bash
set -euo pipefail

DEMO_DIR=$(mktemp -d)
trap 'rm -rf "$DEMO_DIR"' EXIT

mkdir -p "$DEMO_DIR/repo-alpha" "$DEMO_DIR/repo-beta" "$DEMO_DIR/repo-delta"
echo 'msg = "hello"'         > "$DEMO_DIR/repo-alpha/alpha.py"
echo 'greeting = "hello"'    > "$DEMO_DIR/repo-beta/beta.py"
echo 'farewell = "bye"'      > "$DEMO_DIR/repo-beta/gamma.py"
echo 'constant = "hello"'    > "$DEMO_DIR/repo-delta/delta.py"

ROOTS=$(python3 -c "import json,sys; \
  roots=['$DEMO_DIR/repo-alpha','$DEMO_DIR/repo-beta','$DEMO_DIR/repo-delta','/nonexistent/repo-epsilon']; \
  print(json.dumps(roots))")

TRANSFORMS='[{"raw_query":"(string) @s","capture":"s","action":"replace","code":"\"world\""}]'

# Initialize sessions for each repo first.
for root in "$DEMO_DIR/repo-alpha" "$DEMO_DIR/repo-beta" "$DEMO_DIR/repo-delta"; do
  curl -s -X POST http://127.0.0.1:8765/mcp \
    -H "Content-Type: application/json" \
    -d "{\"method\":\"tools/call\",\"params\":{\"name\":\"parse\",\"arguments\":{\"path\":\"$root\"}}}" \
    > /dev/null
done

# Run the multi-root transform.
curl -s -X POST http://127.0.0.1:8765/mcp \
  -H "Content-Type: application/json" \
  -d "{\"method\":\"tools/call\",\"params\":{\"name\":\"transform_multi_root\",\"arguments\":{\"roots\":$ROOTS,\"transforms\":$TRANSFORMS}}}" \
  | python3 -m json.tool
```

---

## Implementation notes

- The implementation lives in `go/mcp/multi_root.go`.
- `applySpecsToRoot` captures errors per-root into `RootDiffBundle.Error`
  rather than returning them, which is the isolation mechanism.
- The tool uses `directLoader` (which calls `model.Load`) when no custom
  loader is injected — the same loader the model pool uses in production.
- `transform_multi_root` is safe to call concurrently with single-root
  sessions because it never touches the calling `Handler`'s `h.pending`
  or `h.model` fields.

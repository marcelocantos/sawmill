# Demo: apply_multi_root_pr — Cross-Repo PR Creation

This document demonstrates the `apply_multi_root_pr` MCP tool end-to-end
across four project roots, including per-repo PR creation and graceful error
isolation when one root fails at the push step.

---

## What the tool does

`apply_multi_root_pr` accepts a list of `{root, diff}` bundles (typically
produced by `transform_multi_root`) plus PR metadata templates. For each
bundle it:

1. Applies the diff to the working tree via `git apply --index`.
2. Creates or reuses a feature branch (`git checkout -b <branch>`).
3. Stages all changes (`git add -A`) and commits.
4. Pushes the branch to `origin` (`git push -u origin <branch>`).
5. Opens a GitHub PR via `gh pr create`, or returns the URL of an existing
   open PR for that head branch (idempotency).

The per-repo result shape:

```json
{
  "branch":     "sawmill/rename-myrepo",
  "commit_sha": "abc123...",
  "pr_url":     "https://github.com/org/myrepo/pull/42",
  "error":      ""    // non-empty only on per-repo failure
}
```

Key properties:

- **Per-repo error isolation.** A root that fails at any stage (push
  permission, bad remote, `gh` auth, etc.) records its error in `bundle.error`
  and the call continues for the remaining roots. No aggregate short-circuit.
- **Templating.** `branch_template`, `title_template`, `body_template`, and
  `commit_message` support `{root}` (absolute path) and `{repo}` (basename)
  placeholders.
- **Idempotency.** If the branch already exists, the diff is committed on top
  of it. If a PR is already open for the head branch, its URL is returned
  instead of creating a duplicate.

---

## Auth assumption

> **Important:** this tool shells out to `gh pr create` and `gh pr list`.
> It requires `gh auth login` to have been run before calling the tool.
> If `gh` is not authenticated, the per-repo `error` field will contain a
> `gh pr create: …` message and `pr_url` will be empty. All git operations
> up to and including the push will still have succeeded, so the branch and
> commit are preserved for a manual retry.

Git operations (`git apply`, `git checkout`, `git add`, `git commit`,
`git push`) use the system `git` CLI and rely on the user's configured
credential helper or SSH agent — the same as a terminal `git push` would.

---

## Scenario

We have four roots:

| Root | Contents | Expected outcome |
|------|----------|-----------------|
| `repo-alpha` | `alpha.py` with `x = "hello"` | branch + commit + PR opened |
| `repo-beta` | `beta.py` and `gamma.py` with `"hello"` | branch + commit + PR opened |
| `repo-delta` | `delta.py` with `"hello"` | branch + commit + PR opened |
| `repo-epsilon` | origin set to `/nonexistent/…` | push fails → per-repo error |

The diffs replace every `"hello"` string literal with `"world"` (produced
by `transform_multi_root`).

---

## Typical workflow

```
transform_multi_root → apply_multi_root_pr
```

Step 1 — generate diffs:

```json
{
  "roots": [
    "/work/repo-alpha",
    "/work/repo-beta",
    "/work/repo-delta",
    "/work/repo-epsilon"
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

Step 2 — turn each `RootDiffBundle.diffs` list into a single `diff` string
(join with `"\n"`) and call `apply_multi_root_pr`:

---

## MCP call payload

```json
{
  "bundles": [
    {
      "root": "/work/repo-alpha",
      "diff": "--- a/alpha.py\n+++ b/alpha.py\n@@ -1 +1 @@\n-x = \"hello\"\n+x = \"world\"\n"
    },
    {
      "root": "/work/repo-beta",
      "diff": "--- a/beta.py\n+++ b/beta.py\n@@ -1 +1 @@\n-greeting = \"hello\"\n+greeting = \"world\"\n--- a/gamma.py\n+++ b/gamma.py\n@@ -1 +1 @@\n-farewell = \"bye\"\n+farewell = \"world\"\n"
    },
    {
      "root": "/work/repo-delta",
      "diff": "--- a/delta.py\n+++ b/delta.py\n@@ -1 +1 @@\n-constant = \"hello\"\n+constant = \"world\"\n"
    },
    {
      "root": "/work/repo-epsilon",
      "diff": "--- a/epsilon.py\n+++ b/epsilon.py\n@@ -1 +1 @@\n-value = \"hello\"\n+value = \"world\"\n"
    }
  ],
  "branch_template":  "sawmill/replace-hello-{repo}",
  "title_template":   "chore({repo}): replace hello with world",
  "body_template":    "Automated rename across the fleet.\n\nRepo: {root}",
  "commit_message":   "chore: replace hello with world ({repo})"
}
```

---

## Expected output (abridged)

```
apply_multi_root_pr: 4 root(s), 3 PR(s) opened, 1 root(s) with errors

{
  "/work/repo-alpha": {
    "branch": "sawmill/replace-hello-repo-alpha",
    "commit_sha": "a1b2c3d...",
    "pr_url": "https://github.com/org/repo-alpha/pull/7"
  },
  "/work/repo-beta": {
    "branch": "sawmill/replace-hello-repo-beta",
    "commit_sha": "e4f5a6b...",
    "pr_url": "https://github.com/org/repo-beta/pull/12"
  },
  "/work/repo-delta": {
    "branch": "sawmill/replace-hello-repo-delta",
    "commit_sha": "c7d8e9f...",
    "pr_url": "https://github.com/org/repo-delta/pull/3"
  },
  "/work/repo-epsilon": {
    "branch": "sawmill/replace-hello-repo-epsilon",
    "commit_sha": "f0a1b2c...",
    "error": "git push: exit status 128\nfatal: '/nonexistent/does-not-exist.git' does not appear to be a git repository"
  }
}
```

Key observations:
- Three of four roots produce PR URLs.
- `repo-epsilon` fails at the push step but records the branch and commit SHA
  so the user can fix the remote and push manually.
- The summary line reports 4 root(s), 3 PR(s) opened, and 1 root(s) with
  errors.

---

## Runnable Go tests

The unit tests in `go/mcp/multi_root_pr_test.go` exercise this scenario
programmatically using ephemeral local bare repos as push targets:

```bash
cd go
go test ./mcp/... -run TestApplyMultiRootPR -v -count=1
```

Expected output:

```
=== RUN   TestApplyMultiRootPRTwoRepos
--- PASS: TestApplyMultiRootPRTwoRepos (1.5s)
=== RUN   TestApplyMultiRootPRThreeRoosWithError
--- PASS: TestApplyMultiRootPRThreeRoosWithError (1.5s)
=== RUN   TestApplyMultiRootPRIdempotentBranch
--- PASS: TestApplyMultiRootPRIdempotentBranch (0.9s)
=== RUN   TestApplyMultiRootPRTemplating
--- PASS: TestApplyMultiRootPRTemplating (0.0s)
=== RUN   TestApplyMultiRootPRBadRoot
--- PASS: TestApplyMultiRootPRBadRoot (0.0s)
=== RUN   TestHandleApplyMultiRootPRIntegration
--- PASS: TestHandleApplyMultiRootPRIntegration (0.7s)
PASS
ok      github.com/marcelocantos/sawmill/mcp    5.0s
```

The tests verify:
1. `TestApplyMultiRootPRTwoRepos` — two repos produce branches, commits, and PR URLs.
2. `TestApplyMultiRootPRThreeRoosWithError` — N=3: two succeed, one fails at push
   with a clear error; siblings are unaffected.
3. `TestApplyMultiRootPRIdempotentBranch` — branch reuse works (no error on
   `git checkout -b` when branch exists); second full run also succeeds.
4. `TestApplyMultiRootPRTemplating` — `{root}` and `{repo}` placeholders replaced.
5. `TestApplyMultiRootPRBadRoot` — non-git directory surfaces a clear error without
   calling the PR opener.
6. `TestHandleApplyMultiRootPRIntegration` — full MCP handler dispatch path works;
   accepts either a PR URL (if `gh` is authenticated) or a per-repo gh error.

---

## Shell demo script

The script below creates four fixture repos, runs a `transform_multi_root`
call to generate diffs, and then calls `apply_multi_root_pr`. It requires:
- `sawmill serve` running on `127.0.0.1:8765`
- `gh auth login` already done
- `jq` installed

```bash
#!/usr/bin/env bash
# Demo: transform_multi_root | apply_multi_root_pr
set -euo pipefail

DEMO_DIR=$(mktemp -d)
trap 'rm -rf "$DEMO_DIR"' EXIT

# Create four working trees, each with a bare repo as origin.
for repo in alpha beta delta; do
  git init --bare "$DEMO_DIR/$repo.git"
  git init -b master "$DEMO_DIR/$repo"
  git -C "$DEMO_DIR/$repo" config user.email "demo@example.com"
  git -C "$DEMO_DIR/$repo" config user.name "Demo"
  git -C "$DEMO_DIR/$repo" remote add origin "$DEMO_DIR/$repo.git"
done

echo 'x = "hello"'         > "$DEMO_DIR/alpha/alpha.py"
echo 'greeting = "hello"'  > "$DEMO_DIR/beta/beta.py"
echo 'farewell = "bye"'    > "$DEMO_DIR/beta/gamma.py"
echo 'constant = "hello"'  > "$DEMO_DIR/delta/delta.py"

for repo in alpha beta delta; do
  git -C "$DEMO_DIR/$repo" add -A
  git -C "$DEMO_DIR/$repo" commit -m "initial"
  git -C "$DEMO_DIR/$repo" push -u origin master
done

# Step 1: generate diffs via transform_multi_root.
ROOTS=$(python3 -c "
import json, sys
print(json.dumps([
  '$DEMO_DIR/alpha', '$DEMO_DIR/beta', '$DEMO_DIR/delta',
  '/nonexistent/repo-epsilon'
]))")

TRANSFORMS='[{"raw_query":"(string) @s","capture":"s","action":"replace","code":"\"world\""}]'

TRANSFORM_RESULT=$(curl -s -X POST http://127.0.0.1:8765/mcp \
  -H "Content-Type: application/json" \
  -d "{\"method\":\"tools/call\",\"params\":{\"name\":\"transform_multi_root\",\"arguments\":{\"roots\":$ROOTS,\"transforms\":$TRANSFORMS}}}" \
  | jq -r '.result.content[0].text')

echo "=== transform_multi_root ==="
echo "$TRANSFORM_RESULT"

# Step 2: build bundles from the diffs (join per-root diffs with newline).
BUNDLES=$(python3 -c "
import json, sys
data = json.loads('''$TRANSFORM_RESULT'''[json.loads('''$TRANSFORM_RESULT''').index('{'):])
bundles = []
for root, b in data.items():
    if b.get('error'):
        continue
    bundles.append({'root': root, 'diff': '\n'.join(b['diffs'])})
print(json.dumps(bundles))")

# Step 3: open PRs.
# NOTE: gh pr create will target whatever 'origin' is configured in each repo.
# In this demo the bare repos are local so gh will fail — expected.
PR_RESULT=$(curl -s -X POST http://127.0.0.1:8765/mcp \
  -H "Content-Type: application/json" \
  -d "$(python3 -c "
import json
payload = {
  'method': 'tools/call',
  'params': {
    'name': 'apply_multi_root_pr',
    'arguments': {
      'bundles': json.dumps($BUNDLES),
      'branch_template': 'sawmill/replace-hello-{repo}',
      'title_template': 'chore({repo}): replace hello with world',
      'body_template': 'Automated rename.\n\nRepo: {root}',
    }
  }
}
print(json.dumps(payload))")" \
  | jq -r '.result.content[0].text')

echo "=== apply_multi_root_pr ==="
echo "$PR_RESULT"
```

---

## Implementation notes

- Implementation: `go/mcp/multi_root_pr.go`.
- Tool registration: `go/mcp/server.go` (Call switch + Definitions).
- Git operations use the system `git` CLI (not go-git) to ensure credential
  helper / SSH agent compatibility.
- PR operations shell out to `gh pr create` / `gh pr list`.
- `applyDiffAndOpenPRWith` (used by tests) accepts an injectable PR-opener
  function to bypass `gh` without a stub server.
- Error isolation: each per-repo step returns `RootPRBundle.Error` on
  failure rather than propagating, matching `transform_multi_root`'s pattern.

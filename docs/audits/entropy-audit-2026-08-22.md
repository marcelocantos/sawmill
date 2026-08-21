# Entropy audit — sawmill

Date: 2026-08-22
Mode: full (entropy + hygiene)
Auditor: entropy-audit owner (this campaign)

## Executive summary

- **Snapshot:** `/Users/marcelo/work/github.com/marcelocantos/sawmill`
- **Branch:** `fix/parse-deadline` (tracks `origin/fix/parse-deadline`)
- **HEAD:** `27b80d454fb3629e6faa81f8af5a59a6b6e8f4cc` (`27b80d4 Update bullseye.yaml`)
- **Initial dirty state:** clean (`git status --porcelain=v1 -b` showed only the branch header)
- **Date:** 2026-08-22
- **Scope:** whole repository. Implementation language is Go 1.26 (module `github.com/marcelocantos/sawmill` under `go/`). Product languages (Python, Rust, …) are adapter payloads, not this repo's code.
- **Exclusions:** `bin/sawmill` and `go/sawmill` binaries (gitignored); merge fixtures under `go/merge/testdata/`; empty untracked `go/go/merge/testdata/` tree (not in git); parent workspace file `/Users/marcelo/work/github.com/marcelocantos/go.work` (environmental, not a sawmill source file).

**Headline mechanism.** Sawmill is a layered, acyclic Go daemon: adapters → forest/store/index → model/modelpool → mcp/daemon → `cmd/sawmill`. Entropy is not a missing architecture; it is a **68-tool MCP hub whose write confinement, public-surface catalogue, and CI ratchets did not grow with the product.** Fable-5's apply/undo/race defects were repaired with named regression tests. The remaining load-bearing gaps are (1) codegen/apply still write caller-supplied paths with no root join or confinement, (2) the tool catalogue is restated in five places that already disagree, and (3) the only CI gate is `go test -race`.

**Highest-consequence findings.**

- **ENT-001 (P1):** `ctx.addFile` stores the JS path verbatim; `apply` writes it. The Homebrew service `working_dir` is `HOMEBREW_PREFIX`, so the documented relative `addFile` lands outside the project.
- **ENT-003 (P2):** STABILITY.md claims 68 tools but tables 59 (the entire discovery tier is missing); CLAUDE.md still says 66; root `agents-guide.md` and `go/mcp/agents-guide.md` are two copies with a release-only sync.
- **ENT-008 (P2):** CI does not run `gofmt` or `go vet`. The tree currently has 32 unformatted Go files; `go vet` fails on unreachable code in `watcher_test.go`.

**Unverified residue.** Live two-volume EXDEV; whether the published tap formula still sets `working_dir HOMEBREW_PREFIX`; `govulncheck` (not installed); golangci-lint without a repo config; Windows. See Oracle coverage.

## Scope and exclusions

In scope: `go/` packages, MCP tool surface, CI/release workflows, Homebrew formula, agent/docs/stability contract, SQLite store, JS transform/codegen runtimes.

Named exclusions:

| Path | Role | Why skipped as production code |
|---|---|---|
| `go/merge/testdata/` | merge fixtures | input corpora, not product logic |
| `go/forest/testdata/pathological_glr.sh` | parse-timeout fixture | adversarial input |
| `bin/`, `go/sawmill` | build artefacts | gitignored |
| `go/go/merge/testdata/` | empty local dirs | not git-tracked |
| `docs/audit-2026-04-05.md`, `docs/audit/fable-2026-07.md` | prior audits | evidence sources, not current code |

`docs/TODO.md` and `docs/design.md` are in scope as governance/docs (they are competing or stale authorities).

## Commands run

All Go commands used `GOWORK=off`. Without it, `go list ./...` from `go/` fails because Go walks up to `/Users/marcelo/work/github.com/marcelocantos/go.work`, which lists only `./claudia` and `./jevons` (see residue).

| Command | Version / notes | Exit | Shipped vs auxiliary | Limitations |
|---|---|---|---|---|
| `git rev-parse HEAD`; `git status --porcelain=v1 -b` | git 2.55.0 | 0 | provenance | — |
| `go version` | go1.26.4 darwin/arm64 | 0 | toolchain | — |
| `go list ./...` and internal import graph via `go list -f '{{.ImportPath}} {{join .Imports " "}}'` | same | 0 | shipped graph | compile-time imports only |
| `gofmt -l .` (excluding testdata) | gofmt from go1.26.4 | 0; **32 dirty files** | auxiliary (not in CI) | style, not behaviour |
| `go vet ./...` | same | **1** (`watcher/watcher_test.go:46:2: unreachable code`) | auxiliary (not in CI) | — |
| `make test` (`cd go && go test ./... -count=1 -race`) | Makefile target; mirrors `.github/workflows/go.yml` | **0**; wall ~286s (`gitindex` 276s) | **shipped path** | `gitindex` indexes this checkout's full history |
| `go test ./... -count=0` | compile-only inventory | 0 | auxiliary | — |
| Call vs `Definitions()` name extraction | Python regex on `go/mcp/server.go` | 0; 68/68 names match | auxiliary | does not check schemas |
| STABILITY.md tool-table vs `Definitions()` | Python regex | 0; 9 tools in code not in the table | auxiliary | — |
| `diff -q agents-guide.md go/mcp/agents-guide.md` | — | 0 (identical) | auxiliary | identity now ≠ identity later |
| `~/.claude/skills/hygiene/hygiene_check.py` | uv-run hygiene validator | FileNotFoundError: no `hygiene.yaml` | hygiene | undeclared posture |
| `golangci-lint` / `staticcheck` | 2.12.2 / 2025.1.1 installed locally | not invoked on the repo | — | no repo config; not a CI gate |
| `govulncheck` | absent | not run | — | no vulnerability oracle |

`make test` packages with no test files: `modelpool`, `paths`, `tscompat`. 461 `func Test` functions; 12 `t.Skip` sites, all conditional (unix perms, git, `testing.Short`, live Ollama).

## Dimension vector

| Dimension | State | Evidence summary | Change from baseline |
|---|---|---|---|
| Architecture topology | healthy | Acyclic internal imports; `model` is the composition root; `mcp` is the intended fan-in hub; adapters isolated behind `LanguageAdapter` | n/a (first contract-form audit; Fable-5 was defect-oriented) |
| Redundancy / sources of truth | concern | Tool catalogue restated in Call switch, `Definitions()`, STABILITY.md, CLAUDE.md, README, two agents-guides; STABILITY table already drifted | n/a |
| Change amplification | concern | `go/mcp/tools.go` (5033 lines) and `server.go` co-change on every tool; 19 and 18 commits respectively | n/a |
| Local code quality | concern | 32 `gofmt`-dirty files; `go vet` fail; otherwise idiomatic Go, no TODO/FIXME in `*.go` | n/a |
| Correctness / verification | concern | Shipped `-race` tests green including e2e; Fable-5 regressions present; codegen-apply paths, EXDEV, mixed literals, JS timeout uncovered | n/a |
| Security / dependencies | concern | Loopback-by-default; `rename_file` confined; codegen/apply not; `--addr` unrestricted; parameterized SQL; no vuln scanner | n/a |
| Build / release / operations | concern | CI = build + `go test -race` on Ubuntu only; release omits `-race`; `gitindex` dominates test time; JS unbounded vs parse deadline | n/a |
| Documentation / governance | concern | STABILITY/CLAUDE/design drift; `hygiene.yaml` absent; leftover `docs/TODO.md`; no CODEOWNERS/dependabot | n/a |

Do not aggregate these states into a scalar.

## Observed architecture

### Entry points and deployable units

- CLI: `go/cmd/sawmill/main.go` — `serve`, `merge`, `merge-driver`, `version`, `--help-agent`.
- HTTP MCP daemon: `daemon.Server` on `127.0.0.1:8765/mcp` (streamable HTTP, mcp-go), optional pprof on `127.0.0.1:8766`.
- Homebrew formula + launchd service (`homebrew/sawmill.rb`).
- One Go module (`go/go.mod`); no submodules.

### Package topology (observed, compile-time)

Direction is downward. No internal import cycles were found.

```
cmd/sawmill → daemon, mcp, merge, adapters, paths
daemon      → mcp, model, modelpool
mcp         → adapters, bisect, codegen, diffharness, exemplar, forest,
              gitindex, gitrepo, jsengine, lspclient, merge, model,
              rewrite, semdiff, store, transform, tscompat
model       → adapters, embed, forest, gitindex, gitrepo, index,
              lspclient, paths, scope, store, summary, tscompat, watcher
modelpool   → model
forest      → adapters, paths, store, tscompat
adapters    → tscompat
```

`mcp` is the high fan-in hub (16 internal imports). `model` is the high fan-out composition root. That matches the declared "one model per project root, per-session pending/backups" design in `CLAUDE.md` / `README.md`.

### Declared vs observed

| Rule | Status |
|---|---|
| HTTP MCP on loopback; stdio via gateway | declared and observed (`paths.DefaultListenAddr`, `daemon.Start`) |
| `parse` binds a session to a root; modelpool shares models | declared and observed |
| Transforms preview; writes only on `apply` | declared and observed (`handleApply` requires `confirm`) |
| Pure Go (gotreesitter, modernc sqlite/quickjs) | declared and observed (`go/go.mod`) |
| `LanguageAdapter` per language, no unified AST | declared and observed; `adapters/capabilities.go` is the agent-facing capability SoT |
| Scope-aware rename (Go/Python) | declared (v0.18) and present under `go/rewrite/scope*.go` |
| AST merge full only for Python and Go | declared (`STABILITY.md`, daemon instructions) and observed (`go/merge`) |
| `rename_file` destination confined to root | observed (post-Fable); **not** generalised to all writers |
| Staging/backup as `rel.bak` / `rel.new` | **contradicted**: `paths.NewApplyDir` uses unique `apply-*` dirs; STABILITY still lists `.bak`/`.new` suffixes as **Stable** |
| Design doc "Tree-sitter Rust crate" | **contradicted** (`docs/design.md` is the pre-Go rewrite) |
| MCP tool count 66 / 68 / 59 | **contradicted** across CLAUDE / README+STABILITY header / STABILITY table |

### Runtime data

Persistent state lives under `~/.sawmill/` (`paths.StoreDir` / `BackupDir`). The SQLite store owns files, symbols, FTS, vectors, recipes, conventions, invariants, concepts. Schema evolution is additive `ALTER TABLE` in `store.init`, not sqlift.

## Findings

### ENT-001: Codegen `addFile` and apply write caller paths with no root join or confinement

- **Priority:** P1
- **Dimensions:** Correctness / verification; Security / dependencies; Architecture topology
- **Status:** observed fact
- **Evidence:**
  - `go/codegen/codegen.go:44-46` stores `path` as given; `:231-233` registers `__addFile` with no `filepath.Join(root, …)`; `:306-312` emits `forest.FileChange{Path: path}` for new files.
  - Production tracked files are absolute (`forest.FileAccessor.Path` documents absolute paths; `model.Load` walks `filepath.Abs` roots). New files therefore do not share the path convention of edits.
  - Tests lock in the relative form: `go/codegen/codegen_test.go:137-147` expects `changes[i].Path == "new_module.py"`.
  - `go/mcp/tools.go:1303-1304` calls `forest.ApplyWithBackup(h.model.Root, h.pending.Changes)` and never rewrites or confines `change.Path`. `ApplyWithBackup` (`go/forest/forest.go:287`) `os.Rename`s onto `change.Path` as-is.
  - The only confinement helper is `handleRenameFile` (`go/mcp/tools.go:2137-2145`). Grep for `escapes the project root` hits that site only.
  - Agents are told to call `ctx.addFile(path, content)` (`agents-guide.md:354`) with no "must be absolute under the project root" caveat.
  - Homebrew service sets `working_dir HOMEBREW_PREFIX` (`homebrew/sawmill.rb:43`). Relative `addFile("new_module.py")` + `apply` therefore writes `/opt/homebrew/new_module.py` (or `/usr/local/…`) on the shipped brew-services path. `filepath.Join` with `..` or an absolute path also escapes, the Fable-5 `rename_file` class of bug.
- **Mechanism:** Write confinement was added for one tool (`rename_file`) after Fable-5 F2. It was not extracted into a shared `withinRoot` predicate and was not applied to codegen new files (or to `ApplyWithBackup` itself). Preview/apply will happily write wherever the path points; brew CWD is not the project.
- **Blast radius:** Any `codegen` program that creates files, then `apply`. Existing-file edits (absolute store paths) are unaffected. Escape also allows overwriting files outside the bound root with no backup of the victim (new files have `Original: nil`).
- **Counterevidence checked:** `rename_confine_test.go` covers `rename_file` only. e2e covers parse/query/rename/transform apply, not codegen `addFile`. `clone_and_adapt` targets a tracked accessor (`tools.go:2341-2355`) so it stays in-root. `extract_to_env` joins `.env.example` / `.gitignore` onto `root`.
- **Smallest coherent remediation:** One `confinePath(root, p) (abs string, err error)` used by every writer. For `addFile`, join relative paths onto the model root, reject `..` and already-existing destinations, and store the absolute path on `FileChange`. Add an apply-time assertion that every `change.Path` and rename target is inside `root`. Fix the brew working directory to a writable data dir or the user's home, not `HOMEBREW_PREFIX`.
- **Verification:** Test: session bound at `t.TempDir()`, `ctx.addFile("x.py", …)` then `apply(confirm=true)` — file must exist at `$root/x.py`, not CWD. Second test: `addFile("../victim", …)` must error and leave the sibling file intact. Both fail today for the relative-success case (writes CWD) and the escape case.
- **Ratchet candidate:** Architecture test: every `forest.FileChange` produced by MCP handlers has `filepath.Rel(root, path)` with no `..` prefix. CI job or `go test` in `mcp`/`codegen`.

### ENT-002: Dual MCP tool registry with no parity oracle

- **Priority:** P2
- **Dimensions:** Change amplification; Redundancy / sources of truth
- **Status:** observed fact (currently consistent; unenforced)
- **Evidence:**
  - Dispatch: `Handler.Call` switch (`go/mcp/server.go:110-250`) — 68 `case` names.
  - Schema/registration: `Definitions()` (`go/mcp/server.go:287` ff.) — 68 `mcpgo.NewTool` names.
  - `RegisterTools` (`:260-264`) iterates `Definitions()` then calls `h.Call(name, …)`. A name in one list but not the other is either an uncallable advertised tool or a hidden handler.
  - No test asserts the two name sets are equal. `TestLanguagesToolRegistered` (`go/mcp/languages_test.go:77-90`) checks a single name.
  - Adding a tool also requires STABILITY.md, agents-guide.md (twice), often README and CLAUDE.md — see ENT-003.
  - Churn: `git log -- go/mcp/tools.go` 19 commits; `server.go` 18. Highest-churn Go files with `model.go`.
- **Mechanism:** Two hand-maintained enumerations of the same public surface. They happen to match today (regex extract: only-Call ∅, only-Defs ∅). The next tool added in `tools.go` but omitted from `Definitions()` never appears to MCP clients; the reverse advertises a tool that returns `unknown tool`.
- **Blast radius:** Every new MCP tool; agent clients that trust `tools/list`.
- **Counterevidence checked:** Names currently match. `RegisterTools` is the only registration path.
- **Smallest coherent remediation:** Generate the switch from `Definitions()`, or keep both and add `TestCallAndDefinitionsParity` ranging `Definitions()` and a exported name list from `Call`.
- **Verification:** Delete one `case` or one `NewTool` — a parity test must fail. Today `make test` still passes.
- **Ratchet candidate:** `go test` in package `mcp` comparing the two name sets; optionally compare to a frozen list in STABILITY.md.

### ENT-003: Competing catalogues for the public tool surface and agent guide

- **Priority:** P2
- **Dimensions:** Redundancy / sources of truth; Documentation / governance; Change amplification
- **Status:** observed fact
- **Evidence:**
  - Code: 68 tools (`Definitions()` / `Call`).
  - `STABILITY.md:26` header: "MCP tools (68 tools)". The markdown table of tools contains **59** ``| `name` `` rows. Missing from the table, present in code: `search_code`, `semantic_search`, `find_by_concept`, `teach_concept`, `list_concepts`, `delete_concept`, `graph_expand`, `central_symbols`, `index_status` (the v0.18 discovery tier; documented in README and agents-guide).
  - `CLAUDE.md:28` / `Claude.md:28`: "MCP server (66 tools)".
  - `README.md:141`: "68 tools".
  - Two agent guides, currently byte-identical (`diff -q` silent; 612 lines each): repo-root `agents-guide.md` and `go/mcp/agents-guide.md` (`//go:embed` at `go/mcp/tools.go:37-42`).
  - Sync is **release-only**: `.github/workflows/release.yml:39-40` `cp ../agents-guide.md mcp/agents-guide.md`. `Makefile` `build` does not copy. Local `make build` embeds whatever is in `go/mcp/`, not necessarily the root file humans edit.
  - `STABILITY.md:98-99` still declares backup/staging suffixes `.bak` / `.new` **Stable**; implementation is `paths.NewApplyDir` unique directories with `N.new` / `N.bak` (`go/paths/paths.go:61-77`, `go/forest/forest.go:255-269`).
  - `docs/design.md:116-124` still describes the Tree-sitter **Rust crate**, `PathBuf`, and a Rust `LanguageAdapter` trait. The product has been Go since the rewrite (audit-log v0.11.0 🎯T7.0).
- **Mechanism:** The public contract is copied by hand into five artefacts. Header counts get bumped; tables and CLAUDE.md do not. The embed copy can silently diverge from the root guide on any non-release commit.
- **Blast radius:** Agents reading STABILITY or CLAUDE miss the discovery tools or call a count that `tools/list` contradicts. 1.0 freeze (`STABILITY.md` "Gaps and prerequisites") is tracking an incomplete table.
- **Counterevidence checked:** README and agents-guide **do** list the discovery tools. The two guides are identical **at this snapshot**.
- **Smallest coherent remediation:** One generated or tested catalogue (code `Definitions()` is the authority). STABILITY table generated or parity-tested against it. Embed exactly one `agents-guide.md` (Makefile copy or embed the root file). Mark `docs/design.md` historical or rewrite it against the Go daemon.
- **Verification:** Test that STABILITY table names == `Definitions()` names; CI `diff -q` the two guides (or stop storing two).
- **Ratchet candidate:** `go test` parsing STABILITY.md tool names; `diff` of the two guides in `make test`.

### ENT-004: Apply stages in `~/.sawmill` then `os.Rename`s into the project (EXDEV)

- **Priority:** P2
- **Dimensions:** Correctness / verification; Build / release / operations
- **Status:** observed fact (failure mode needs a two-volume host to fire)
- **Evidence:**
  - Staging files are created under `paths.NewApplyDir(root)` → `~/.sawmill/backups/<hash>/apply-*` (`go/forest/forest.go:240-255`).
  - Install step is `os.Rename(tempPaths[i], change.Path)` (`:287-289`). No `EXDEV` / `syscall.EXDEV` handling exists anywhere in `*.go`.
  - Fable-5 F11 described this; unique apply dirs fixed concurrent-path clobber (F9) but kept cross-volume staging.
  - `STABILITY.md:165` lists Windows packaging as out of scope but not cross-volume Mac/Linux projects.
- **Mechanism:** Rename is only atomic on the same filesystem. A project on an external disk, second APFS volume, or container mount cannot apply any transform; the first rename fails and rollback runs.
- **Blast radius:** Every `apply` for a root not on the same volume as `$HOME`.
- **Counterevidence checked:** Same-volume apply is well tested (`go/forest/backup_test.go`, e2e). Per-apply unique dirs are a real improvement over deterministic `.bak` paths.
- **Smallest coherent remediation:** Stage next to the target (`change.Path + ".sawmill-tmp"`) or on EXDEV fall back to copy+fsync+replace in the target directory; keep backups under `~/.sawmill`.
- **Verification:** Apply on a disk image / second volume must succeed. Unit test can mock rename failure with `EXDEV` if the fallback is a helper.
- **Ratchet candidate:** Test injecting `EXDEV` from a fake rename, or a documented manual attestation until a CI volume exists.

### ENT-005: `add_field` still mixes keyed initializers into positional literals

- **Priority:** P2
- **Dimensions:** Correctness / verification; Local code quality
- **Status:** observed fact (Fable-5 F12, not remediating)
- **Evidence:**
  - `go/mcp/add_field.go:254-274`: any existing `literal_element` (Go positional composite) still triggers `Replacement: ", " + initText` where `initText` is `GenFieldInitializer` (keyed `name: value` for Go, `go/adapters/go_lang.go:182`).
  - No test mentions `literal_element`, "positional", or "mixture".
  - Fable-5 F12: `Foo{1, 2}` + add field `c` → `Foo{1, 2, c: 0}` which Go rejects.
- **Mechanism:** Literal-style detection was never added. The same append path serves keyed and positional literals.
- **Blast radius:** `add_field` on Go (and similarly positional C++/Java generator paths) for types constructed with positional literals.
- **Counterevidence checked:** `add_field.go` does distinguish `keyed_element` vs `literal_element` for *existence*, not for *shape*. Commit `0b02c94` remediations did not include F12.
- **Smallest coherent remediation:** If the literal has any `literal_element` and no keyed elements, append a positional value (`default_value` only). If mixed, refuse with a structured error. Prefer skipping call sites / literals that would not compile.
- **Verification:** Fixture `type Foo struct{A, B int}; var x = Foo{1, 2}` + `add_field` must either produce `Foo{1, 2, 0}` or error; never `Foo{1, 2, c: 0}`. `go test` the applied file with `go/types`.
- **Ratchet candidate:** `go/mcp` test on a positional literal; optionally `go/parser` the result.

### ENT-006: Pattern captures still split inside string and character literals

- **Priority:** P2
- **Dimensions:** Correctness / verification
- **Status:** observed fact
- **Evidence:**
  - Bracket depth was added (`go/mcp/pattern.go:104-132`) with a regression test for `foo(bar(1, 2), 3)` (`go/mcp/pattern_nesting_test.go:13-29`).
  - The scanner does not skip `"…"`, `'…'`, or `` `…` ``. `foo("a, b", 3)` against `foo($a, $b)` still binds `$a="\"a"` at the first comma, depth 0.
  - No nesting test uses a quoted comma.
- **Mechanism:** F6 was fixed for `()`, `[]`, `{}` only. The matcher is still a string walk, not a CST walk, so language-shaped tokens inside literals remain split points.
- **Blast radius:** `migrate_pattern`, `apply_equivalence`, `migrate_type` on call-like text with commas inside strings — common in assertions and format calls.
- **Counterevidence checked:** Non-nested and bracket-nested cases have tests. CST matching was the alternative sketched in Fable-5 and was not taken.
- **Smallest coherent remediation:** Track quote state (or match on argument child nodes). Add `foo("a, b", 3)` to `pattern_nesting_test.go`.
- **Verification:** `ParsePattern("foo($a, $b)").Match("foo(\"a, b\", 3)")` → `$a="\"a, b\""`, `$b="3"`.
- **Ratchet candidate:** that unit test.

### ENT-007: QuickJS eval is unbounded; parse is not

- **Priority:** P2
- **Dimensions:** Build / release / operations; Correctness / verification
- **Status:** observed fact
- **Evidence:**
  - Parse is bounded: `MaxParseDuration = 20s` plus content quarantine (`go/forest/parse_guard.go:24-41`, `parse_quarantine.go`). Tests in `parse_timeout_test.go` (including a 12s budget case skipped only under `-short`; `make test` is not `-short`).
  - `jsengine.RunJSTransform` (`go/jsengine/jsengine.go:156-164`) `quickjs.NewVM()` + `vm.Eval` with no interrupt, memory cap, or deadline. User `transformFn` is interpolated into the program (`:100-103`).
  - `codegen.RunCodegen` same pattern (`go/codegen/codegen.go:276-279`): user program executed with no timeout.
  - Daemon pprof exists specifically because a wedged process is otherwise undiagnosable (`go/daemon/debug.go:20-29`; default `127.0.0.1:8766`).
  - `handle*` methods hold `h.mu` (`Handler.Call` → e.g. `handleCodegen` `tools.go:1208-1209`). A JS infinite loop wedges that session until process kill.
- **Mechanism:** The hang that motivated T56 (pathological GLR parse) has a sibling in the JS runtimes that agents are invited to supply (`transform` `transform_fn`, `codegen` `program`, conventions, invariants).
- **Blast radius:** One MCP session (per-handler mutex). Other sessions on other handlers continue; the process stays up but that session is dead and holds any borrowed model until Close.
- **Counterevidence checked:** QuickJS has no host FS except registered callbacks; codegen `addFile`/`editFile` mutate a collector, not disk. That sandbox does not provide termination.
- **Smallest coherent remediation:** `context` deadline around `Eval` (QuickJS interrupt if the binding supports it) plus a memory cap. Same budget philosophy as `MaxParseDuration`.
- **Verification:** `transform_fn = "while(true){}"` must return a tool error within N seconds, not hang `go test`.
- **Ratchet candidate:** `jsengine` / `codegen` timeout tests.

### ENT-008: CI ratchets only `go test -race`; format, vet, and OS matrix are unenforced

- **Priority:** P2
- **Dimensions:** Build / release / operations; Local code quality; Correctness / verification
- **Status:** observed fact
- **Evidence:**
  - `.github/workflows/go.yml:9-24`: Ubuntu only; `go build ./...`; `go test ./... -count=1 -race`. No `gofmt`, `go vet`, staticcheck, golangci, secret scan, or macOS job.
  - `.github/workflows/release.yml:36-37`: `go test ./... -count=1` **without** `-race`, despite CLAUDE.md: "always -race: CI uses it, and omitting it hides races locally".
  - Release matrix is darwin-arm64 / linux-amd64 / linux-arm64; test job is linux-amd64 only. `STABILITY.md:165` defers Windows; macOS is a **release** target.
  - `gofmt -l` (this snapshot, excluding testdata): 32 files, including `go/mcp/tools.go` (import order: `bisect` not sorted).
  - `go vet ./...`: `watcher/watcher_test.go:46:2: unreachable code` (`panic("unreachable")` after an infinite `for`).
  - `scripts/hooks/pre-push` runs `make test` only.
  - No `.golangci.yml`, CODEOWNERS, or dependabot config. `govulncheck` not in CI and not installed here.
- **Mechanism:** After the Rust→Go rewrite, clippy/fmt CI was not replaced with Go equivalents. Drift is already visible (gofmt, vet). Release can ship a race that PR CI would have caught, and the inverse: macOS-only bugs (fsnotify, launchd PATH, brew working_dir) have no CI job.
- **Blast radius:** Every change; macOS brew-service users; race fixes that land between PR and release.
- **Counterevidence checked:** PR CI **does** use `-race` and `fetch-depth: 0` for gitindex. Tests were green on this snapshot (`make test` exit 0).
- **Smallest coherent remediation:** Add `gofmt -l` and `go vet ./...` to `go.yml` and `Makefile test`. Run release tests with `-race` or explicitly accept the gap in STABILITY. Consider a macOS CI job for watcher/fsnotify/paths.
- **Verification:** CI must fail on the current `watcher_test.go` vet issue and on an unsorted import.
- **Ratchet candidate:** those two steps in `.github/workflows/go.yml`; later `hygiene.yaml` `quality.gofmt` / `quality.vet`.

### ENT-009: gitindex tests index the live product history (276s of a 286s suite)

- **Priority:** P2
- **Dimensions:** Build / release / operations; Change amplification
- **Status:** observed fact
- **Evidence:**
  - `go/gitindex/indexer_test.go:72-85` `repoRoot` = `filepath.Join(cwd, "../..")` (the sawmill checkout); skipped only if `.git` is missing.
  - `TestIndexHead` (`:105`) indexes HEAD of **this** repo.
  - `make test` wall clock ~286s; `gitindex` **276.209s**; next slowest `mcp` 45.9s (parallelism means wall ≈ gitindex).
  - CI comment `.github/workflows/go.yml:18`: `fetch-depth: 0  # gitindex tests walk real commit history`.
- **Mechanism:** The oracle's corpus is the product git history. Every sawmill commit enlarges the next `make test` / CI run without a bound. Failures also couple indexer bugs to whatever happens to be in HEAD.
- **Blast radius:** Developer loop and CI duration; grows monotonically.
- **Counterevidence checked:** Real-history tests catch integration issues a synthetic repo might miss. `OpenMemory` keeps the index itself ephemeral.
- **Smallest coherent remediation:** A frozen fixture repo (or a `testdata` git bundle with a fixed commit count) for the heavy tests; keep one optional live-HEAD test behind a `-short` inverse or build tag.
- **Verification:** `go test ./gitindex -count=1 -race` on a bundle must finish in seconds and not depend on sawmill's commit count.
- **Ratchet candidate:** time bound or fixture SHA in the gitindex tests.

### ENT-010: `serve --addr` does not warn on non-loopback; privileged tools are unauthenticated

- **Priority:** P2
- **Dimensions:** Security / dependencies; Build / release / operations
- **Status:** observed fact (default bind is loopback; non-default is a footgun)
- **Status note:** `STABILITY.md:166-167` parks "Remote/networked daemon access" and "Multi-user access control" as out of scope for 1.0. The finding is the missing guardrail, not the local-trust model.
- **Evidence:**
  - Default `127.0.0.1:8765` (`go/paths/paths.go:35`) and pprof `127.0.0.1:8766` (`:41`). Homebrew service passes `--addr 127.0.0.1:8765` (`homebrew/sawmill.rb:28`).
  - `runServe` (`go/cmd/sawmill/main.go:68-77`) passes `--addr` to `srv.Start` with no loopback check or warning.
  - MCP HTTP has no auth. `apply_multi_root_pr` (`go/mcp/multi_root_pr.go:1-34`, `:75`) shells out to `git` and `gh pr create` using the host's credentials.
  - pprof is on by default on a second loopback port (`go/daemon/debug.go:30-50`).
- **Mechanism:** Local-agent trust is the design. Binding `0.0.0.0:8765` (or a LAN address) silently exports parse/apply/codegen and PR creation to the network. Nothing in the CLI or logs flags that.
- **Blast radius:** Any process that can open the MCP port: on loopback, other local users/malware; on a non-loopback bind, the network.
- **Counterevidence checked:** Default and brew paths are loopback. STABILITY records the 1.0 exception. Formatters/LSP use argv arrays, not a shell (`go/rewrite/rewrite.go:202`).
- **Smallest coherent remediation:** If the host is not loopback, refuse to start or require an explicit `--i-accept-lan` flag and log a loud warning. Optionally disable `apply_multi_root_pr` unless a config bit is set.
- **Verification:** `sawmill serve --addr 0.0.0.0:8765` must exit non-zero or print a warning that a test captures.
- **Ratchet candidate:** unit test on the addr-policy helper; hygiene item `security.bind-loopback`.

### ENT-011: Governance leftovers — Rust design doc, completed TODO file, undeclared hygiene

- **Priority:** P3
- **Dimensions:** Documentation / governance
- **Status:** observed fact
- **Evidence:**
  - `docs/design.md` Version 0.5 / April 2026 / Rust types (`:116-134`).
  - `docs/TODO.md` contains only two checked items from the Rust era (`src/mcp.rs` split, `HOMEBREW_TAP_TOKEN`).
  - `CLAUDE.md:52` still lists `docs/targets.md`; that file does not exist (targets live in `bullseye.yaml`).
  - No `hygiene.yaml`. Validator: `FileNotFoundError` on `/Users/marcelo/work/github.com/marcelocantos/sawmill/hygiene.yaml`.
  - `.gitignore:21` lists `docs/convergence-report.md`, but the file **is tracked** (`git ls-files`).
- **Mechanism:** Docs from the Rust product and the first open-source audit were not retired when the Go daemon and bullseye replaced them. Hygiene posture was never declared, so nothing ratchets ENT-008 mechanically.
- **Blast radius:** Agents reading `docs/design.md` or CLAUDE layout plan against a dead stack. No fleet-level hygiene query can see sawmill.
- **Counterevidence checked:** `README.md`, `STABILITY.md`, and `agents-guide.md` describe the HTTP Go daemon correctly (aside from ENT-003).
- **Smallest coherent remediation:** Banner or replace `docs/design.md`; delete `docs/TODO.md`; fix CLAUDE layout; drop the useless gitignore line or untrack the report. Declare `hygiene.yaml` only when the owner wants a floor (do not invent one in this audit).
- **Verification:** `docs/design.md` does not mention `tree-sitter` Rust crates; `docs/TODO.md` absent; CLAUDE layout matches `ls go/`.
- **Ratchet candidate:** file-existence checks once hygiene is declared.

### ENT-012: Dead write helper; untested pool/paths; parent `go.work` breaks the documented test command

- **Priority:** P3
- **Dimensions:** Local code quality; Build / release / operations
- **Status:** observed fact
- **Evidence:**
  - `forest.FileChange.Apply` (`go/forest/forest.go:46-51`) writes with hardcoded `0o644` and is unreferenced (no `.Apply()` callers; apply goes through `ApplyWithBackup`).
  - `modelpool` and `paths` have no `_test.go`. Pool idle-eviction (`go/modelpool/modelpool.go:18`, `:74-80`) is load-bearing for the daemon.
  - From `go/`, unset `GOWORK`: `go env GOWORK` → `/Users/marcelo/work/github.com/marcelocantos/go.work`; `go list ./...` → `directory prefix . does not contain modules listed in go.work`. `Makefile` `test`/`build` do not set `GOWORK=off`. CI is fine (no parent workspace).
- **Mechanism:** Leftover API; missing tests on the sharing layer; Makefile assumes no parent Go workspace.
- **Blast radius:** Dead `Apply` can be mistaken for the safe write path (it is not: no backup, no mode, no stale check). Parent `go.work` makes the README/`make test` path fail on this machine.
- **Counterevidence checked:** This is the same class of trap noted in other sibling repos; the parent `go.work` does not list sawmill. Not a module-path bug inside sawmill.
- **Smallest coherent remediation:** Delete `FileChange.Apply`. Add modelpool Get/Release/evict tests. Prefix Makefile Go recipes with `GOWORK=off`.
- **Verification:** `grep FileChange.Apply` empty; `go test ./modelpool`; `make test` works even when a parent `go.work` exists.
- **Ratchet candidate:** `GOWORK=off` in Makefile; modelpool tests.

## Redundancy and competing-source-of-truth inventory

| Fact | Authorities | Drift now? |
|---|---|---|
| MCP tool list | `Call` switch; `Definitions()`; STABILITY table; STABILITY header; CLAUDE.md; README; agents-guide ×2 | **yes** (59 / 66 / 68) |
| Agent guide text | `agents-guide.md`; `go/mcp/agents-guide.md` (embed); release.yml copy | identical at HEAD; sync is release-only |
| Architecture | README / CLAUDE (Go HTTP daemon); `docs/design.md` (Rust) | **yes** |
| Backup path shape | STABILITY `.bak`/`.new`; `paths.NewApplyDir` | **yes** |
| Language capabilities | `adapters/capabilities.go` `LanguageInfo`; agents-guide; STABILITY languages row | capabilities.go is the live SoT; docs point at `languages` tool — healthy |
| Schema | `store.init` CREATE + ALTER | single owner; not sqlift (fleet convention, accepted here) |
| Targets | CLAUDE.md `docs/targets.md`; actual `bullseye.yaml` | **yes** (missing file) |

Deliberate duplication: per-language adapters (necessary); `rewrite/scope_go.go` vs `scope_python.go` (language-specific binding); merge testdata for Go and Python (independent fixtures).

## Healthy structure worth retaining

- **Acyclic package graph** with a real composition root (`model`) and an explicit session/model split (`daemon` + `modelpool`). Enforced only by the compiler; worth a later `go list` cycle check in CI.
- **Fable-5 remediations with named tests:** empty-file undo (`go/forest/backup_test.go:14-47`), mode preservation (`:49`), stale-content abort (`ErrStaleContent`, `forest.go:179-237`), rename confinement (`go/mcp/rename_confine_test.go`), bracket-aware patterns (`pattern_nesting_test.go`), map snapshot under `vecsMu` (`go/model/retrieve.go:173-196`), per-apply unique dirs (`paths.NewApplyDir`), `LockApply` (`go/model/model.go:72-78`, `tools.go:1294-1296`).
- **Parse budget + quarantine** (`parse_guard.go`, `parse_quarantine.go`) with tests that failed a parser-timeout-only implementation (`parse_timeout_test.go:80-104`).
- **`LanguageAdapter` + `capabilities.go`:** adding a language is adapter-shaped, not a unified AST; `languages` MCP tool is the agent-facing card.
- **Apply/undo contract:** preview in `pending`, write only on `confirm`, backups under `~/.sawmill`, not in the project tree.
- **e2e on the shipped transport:** `go/e2e/e2e_test.go` HTTP MCP parse → query → rename → apply → undo, and transform → apply.
- **SQL:** search/query paths bind user input (`store.go:809-818` `MATCH ?` / `GLOB ?`). `PRAGMA table_info(%s)` (`store.go:358`) interpolates internal table names only.
- **License and notices:** Apache-2.0 `LICENSE`, `THIRD_PARTY_NOTICES.md`.
- **pre-push hook** mirrors CI's `make test`.

## Hygiene posture

**Hygiene posture not declared.** There is no `hygiene.yaml` at the repo root.

Invoked `/Users/marcelo/.claude/skills/hygiene/hygiene_check.py` from the repo root. Result: `FileNotFoundError: …/sawmill/hygiene.yaml`. No per-dimension held tiers, floors, or drift vector exist to validate.

Overlap with entropy (not double-counted as hygiene drift): ENT-008 (missing format/vet/OS/secret-scan gates) and ENT-011 (no declared floors) are exactly what a future `hygiene.yaml` would ratchet. Do not initialize it from this audit.

Entropy findings that are machine-enforceable later: ENT-002 parity test, ENT-003 STABILITY/guide diff, ENT-001 path confinement test, ENT-008 gofmt/vet steps.

## Oracle coverage and residue

| Property | Decided by |
|---|---|
| Unit/integration correctness, data races on exercised paths | shipped `make test` (`go test ./... -count=1 -race`) — **green** at 27b80d4 |
| HTTP MCP parse/rename/transform/apply/undo | shipped `go/e2e` — green |
| Apply/undo empty-file, mode, stale content, rename escape, pattern brackets | shipped named regressions — green |
| Parse deadline | shipped `parse_timeout_test` (12s case runs; `make test` is not `-short`) — green |
| gofmt / go vet | auxiliary this audit — **fail** (32 files; vet unreachable) |
| Tool-registry parity, STABILITY catalogue, path confinement of codegen, EXDEV, mixed literals, JS timeout | **no oracle** |
| Dependency vulnerabilities | **nothing** (`govulncheck` absent, no scanner in CI) |
| Live brew-service `addFile` + apply | **not run** (would decide ENT-001 CWD behaviour) |
| Two-volume apply | **not run** (ENT-004) |
| Parent `go.work` | local environment; Makefile unprotected (ENT-012) |

Failed/skipped checks: `go vet` fail; `gofmt` dirty; hygiene validator no file; govulncheck not installed; golangci not run (no config, not declared).

**Owner residue** (intent, not mechanical follow-up):

1. Is a non-loopback `serve --addr` ever intended before 1.0, or should it be refused?
2. Should `hygiene.yaml` be declared at the current (honest) floors, or is undeclared posture accepted until ENT-008 is fixed?
3. Is indexing the live product repo in `gitindex` tests a deliberate soak, or an accident to replace with a fixture?

## Remediation sequence

1. **Oracle seam for writes:** extract `confinePath`, apply it in `ApplyWithBackup` and `addFile`, add the two tests in ENT-001. Fix brew `working_dir`. This is the only P1.
2. **Catalogue authority:** `TestCallAndDefinitionsParity` + STABILITY table filled from `Definitions()` (or generated). Embed a single agents-guide (Makefile copy or one path). Update CLAUDE.md count/layout.
3. **CI ratchets:** `gofmt -l` and `go vet` on the `go.yml` job and `Makefile test`; `GOWORK=off` in Makefile; release tests with `-race` or an explicit STABILITY exception.
4. **Remaining Fable correctness:** positional `add_field` (ENT-005), quoted commas in patterns (ENT-006), EXDEV fallback (ENT-004), JS deadline (ENT-007).
5. **Decouple gitindex tests from product history** (ENT-009) so CI time stops tracking `git rev-list --count`.
6. **Docs hygiene:** historical banner on `docs/design.md`; delete `docs/TODO.md`. Declare `hygiene.yaml` only after the owner picks floors.
7. Re-run this audit against the same dimension definitions and finding IDs.

No architectural rewrite is required; the layering is sound. The work is confinement, a single tool catalogue, and ratchets on the shipped path.

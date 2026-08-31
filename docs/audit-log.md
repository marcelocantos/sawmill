# Audit Log

Chronological record of audits, releases, documentation passes, and other
maintenance activities. Append-only — newest entries at the bottom.

## 2026-04-05 — /open-source sawmill v0.1.0

- **Commit**: `4eb969c`
- **Outcome**: Open-sourced sawmill. Audit: no secrets or license issues found; 10 clippy lints and 19 compiler warnings fixed. Added Apache 2.0 LICENSE, README.md, CLAUDE.md, agents-guide.md, STABILITY.md. CI workflow (test/clippy/fmt). Release workflow with homebrew-releaser. --help-agent flag. Released v0.1.0 with binaries for darwin-arm64, linux-amd64, linux-arm64.
- **Deferred**:
  - HOMEBREW_TAP_TOKEN secret not yet set on sawmill repo — Homebrew formula push requires manual PAT setup

## 2026-04-05 — /audit

- **Commit**: `83384da`
- **Outcome**: 12 findings (0 critical, 0 high, 5 medium, 5 low, 2 info). Report: docs/audit-2026-04-05.md. Key issues: cargo fmt/clippy failures will block CI; broken LSP timeout implementation; convention checks run on pre-change state.
- **Deferred**:
  - try_read_message broken timeout (medium) — needs non-blocking I/O redesign
  - check_conventions_on_changes pre-change state (medium) — requires re-parsing changed files
  - mcp.rs 2021 lines (low) — module split is cosmetic, not blocking
  - CI multi-OS testing (low) — macOS CI job would increase confidence for cross-platform release
  - HOMEBREW_TAP_TOKEN secret (low) — carried from open-source audit
  - Cargo.lock not committed (info) — recommended for binary crates

## 2026-04-06 — /release v0.2.0

- **Commit**: `70570ae`
- **Outcome**: Released v0.2.0 (darwin-arm64, linux-amd64, linux-arm64). Added agent prompt generation (Frontier K). Fixed convention checking (post-change state), LSP timeout, path handling in undo, stdio safety, unsafe consolidation. CI now tests on macOS + Ubuntu. Homebrew formula updated.
- **Deferred**:
  - HOMEBREW_TAP_TOKEN secret not yet set — Homebrew formula push may fail until configured
  - mcp.rs module split (low) — cosmetic, tracked in docs/TODO.md

## 2026-04-06 — /release v0.5.0

- **Commit**: `36e0e92`
- **Outcome**: Released v0.5.0 (darwin-arm64, linux-amd64, linux-arm64). Renamed project from Canopy to Sawmill (v0.4.0). Split mcp.rs into submodules. HOMEBREW_TAP_TOKEN configured — Homebrew formula updates now fully automated.

## 2026-04-07 — /release v0.7.0

- **Commit**: `157b3cc`
- **Outcome**: Released v0.7.0 (darwin-arm64, linux-amd64, linux-arm64). Refactored to mcpbridge library for proxy↔daemon communication. Moved all persistent state to ~/.sawmill/ (zero project footprint). Fixed --help-agent routing bug. Updated STABILITY.md for current architecture. Homebrew formula updated.

## 2026-04-07 — /release v0.8.0

- **Commit**: `ba7e79f`
- **Outcome**: Released v0.8.0 (darwin-arm64, linux-amd64, linux-arm64). Added 13 new MCP tools (33 total): dependency_usage (T17), teach_invariant/check_invariants/list_invariants/delete_invariant (T19), migrate_type with pattern language (T16), plus LSP tools already in codebase. Updated agents-guide, README, and STABILITY.md. Homebrew formula updated.

## 2026-04-08 — /release v0.9.0

- **Commit**: `172aab0`
- **Outcome**: Released v0.9.0 (darwin-arm64, linux-amd64, linux-arm64). Global daemon architecture (single socket, per-connection handlers). Active model manager (actor pattern, channel-based forest snapshots, no mutexes). Ref-counted ModelPool with 5-minute idle eviction. mcpbridge HandlerFactory cleanup callbacks. All tests pass with -race. Homebrew formula updated.

## 2026-04-22 — /release v0.10.0

- **Commit**: `a717113`
- **Outcome**: Released v0.10.0 (darwin-arm64, linux-amd64, linux-arm64). Major release: tool count grew 33 → 54. Two complete pillars retired (🎯T1 intra-language pattern equivalences, 🎯T3 diagnostic-driven automatic fixes). Other deliverables: 🎯T6 semantic git history (git_log / git_diff_summary / git_blame_symbol with body-vs-signature distinction), 🎯T13 semantic_diff + api_changelog, 🎯T14 git_semantic_bisect, 🎯T24 structured failure payloads (`format=json` on check_conventions / check_invariants / query / diagnostics), 🎯T8.1–T8.3 cross-rep transforms (promote_constant, extract_to_env, migrate_pattern). Architectural rework: dropped mcpbridge / Unix-socket / stdio-proxy in favour of pure HTTP MCP server (mcp-go streamable HTTP transport on `127.0.0.1:8765`). 201 tests under -race. STABILITY.md, README, and agents-guide refreshed for the HTTP architecture and new tool surface. Homebrew formula updated.

## 2026-04-26 — /release v0.11.0

- **Commit**: `a462f4a`
- **Outcome**: Released v0.11.0 (darwin-arm64, linux-amd64, linux-arm64). Tool count 54 → 56. Multi-repo orchestration lands end-to-end: 🎯T5.1 `transform_multi_root` (apply transforms across N project roots in one call) and 🎯T27 `apply_multi_root_pr` (per-repo branches, commits, pushes, and PRs via `git`/`gh`, with per-repo error isolation and idempotency on existing branches/PRs). 🎯T7.0 swapped tree-sitter to the pure-Go `gotreesitter` runtime (no CGo). 🎯T28 added `WithHeartbeatInterval(30s)` to the streamable-HTTP MCP server so idle Claude Code sessions no longer surface `MCP error -32000: Connection closed` after ~3 minutes. STABILITY.md snapshot bumped to v0.11.0; README and agents-guide gained a Multi-repo orchestration section. Homebrew formula updated.

## 2026-04-27 — /release v0.12.0

- **Commit**: `a47d804`
- **Outcome**: Released v0.12.0 (darwin-arm64, linux-amd64, linux-arm64). Tool count 56 → 57. AST-aware three-way merge lands end-to-end across four targets: 🎯T29 `go/merge` engine (declaration-level matching, commute algebra, pure-Go diff3 fallback) covering Python and Go; 🎯T30 `sawmill merge` CLI subcommand (git mergetool driver); 🎯T31 `sawmill merge-driver` CLI subcommand (git low-level driver per gitattributes(5)); 🎯T32 `merge_three_way` MCP tool (stateless, agent-callable). Real `git merge` smoke verified: parallel-method-add merges produce zero conflict markers. 🎯T33 hardened the Homebrew formula: every brew-installable runtime tool the daemon shells out to is now a `depends_on` (`gh`, `llvm`, `prettier`, `pyright`, `ruff`, `typescript-language-server`), and the service block sets a complete PATH so launchd-spawned daemons resolve toolchain-coupled tools (`rustfmt`, `rust-analyzer`, `gopls`, `gofmt`) without `launchctl setenv PATH`. README, agents-guide, and STABILITY.md (snapshot bumped to v0.12.0) refreshed. Homebrew formula updated.
- **Deferred**:
  - Rename detection (delete-then-add of a declaration with unchanged body) — deferred from 🎯T29 acceptance; rename-vs-body case currently produces a textual conflict.
  - TypeScript / Rust / C++ adapter coverage for the merge engine — fall through to whole-file diff3 today.

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

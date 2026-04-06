# Audit Log

Chronological record of audits, releases, documentation passes, and other
maintenance activities. Append-only — newest entries at the bottom.

## 2026-04-06 — /release v0.6.0

- **Commit**: `4a8d4f8`
- **Outcome**: Released v0.6.0 (darwin-arm64, linux-amd64, linux-arm64). Complete Go rewrite replacing Rust codebase. 84 tests, 17 packages. Homebrew formula with service definition. STABILITY.md created with 107 surface items.
- **Deferred**:
  - LSP integration (hover, definition, references, diagnostics) — not yet ported from Rust
  - HOMEBREW_TAP_TOKEN secret — user must create PAT and add secret to repo

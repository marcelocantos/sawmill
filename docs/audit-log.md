# Audit Log

Chronological record of audits, releases, documentation passes, and other
maintenance activities. Append-only — newest entries at the bottom.

## 2026-04-05 — /open-source canopy v0.1.0

- **Commit**: `4eb969c`
- **Outcome**: Open-sourced canopy. Audit: no secrets or license issues found; 10 clippy lints and 19 compiler warnings fixed. Added Apache 2.0 LICENSE, README.md, CLAUDE.md, agents-guide.md, STABILITY.md. CI workflow (test/clippy/fmt). Release workflow with homebrew-releaser. --help-agent flag. Released v0.1.0 with binaries for darwin-arm64, linux-amd64, linux-arm64.
- **Deferred**:
  - HOMEBREW_TAP_TOKEN secret not yet set on canopy repo — Homebrew formula push requires manual PAT setup

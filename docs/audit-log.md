# Audit Log

Chronological record of audits, releases, documentation passes, and other
maintenance activities. Append-only — newest entries at the bottom.

## 2026-04-05 — /open-source canopy v0.1.0

- **Commit**: `d6ba4ea`
- **Outcome**: Open-sourced canopy. Audit: no secrets or license issues found; 10 clippy lints and 19 compiler warnings fixed. Added Apache 2.0 LICENSE, README.md, CLAUDE.md. CI workflow (test/clippy/fmt). Released v0.1.0 with binaries for darwin-arm64, linux-amd64, linux-arm64.
- **Deferred**:
  - HOMEBREW_TAP_TOKEN secret not set on canopy repo — Homebrew formula push will fail until configured
  - No `--help-agent` flag or agents-guide.md

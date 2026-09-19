# AGENTS.md

Guidance for coding agents (and humans) working in this repository.

codex-sync is a Go CLI that syncs the OpenAI Codex home (`~/.codex`, or
`$CODEX_HOME`) across machines through encrypted object storage. It is a fork
of [claude-sync](https://github.com/tawanorg/claude-sync); see `NOTICE`.

Read these first:

- `CLAUDE.md` — build and test commands, architecture, sync semantics, gotchas.
  Everything in it applies to any agent, not only Claude.
- `CONTRIBUTING.md` — workflow, coverage floor, commit style, and the list of
  things that must not change (the Argon2 salt, the SQLite exclusion, the
  protected paths, the no-destructive-defaults rule).
- `docs/specs/2026-09-15-codex-sync-design.md` — why the sync set, merge rules
  and removal logic are the way they are.
- `README.md` — the user-facing behavior you must keep accurate.

Working rules:

- Run `make check` (gofmt, go vet, `go test -short`) before committing; CI also
  enforces 60% coverage on `internal/*` and runs golangci-lint.
- New behavior in `internal/` needs tests. Use the in-memory `mockStorage` in
  `internal/sync/sync_push_pull_test.go` for push/pull scenarios.
- When you change user-visible behavior, update `README.md` and the
  *Unreleased* section of `CHANGELOG.md` in the same change.
- Never run a development build against the real `~/.codex` and sync bucket;
  set `HOME` to a scratch directory (see `CONTRIBUTING.md`).

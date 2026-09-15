# Task Plan: codex-sync — daily encrypted sync of OpenAI Codex local state

## Goal
A `codex-sync` CLI (fork of claude-sync) that pushes/pulls `~/.codex` conversation and
config state between Macs through encrypted cloud storage on a daily schedule, so Codex
work continues on either machine — what claude-sync already does for `~/.claude`.

## Context
- claude-sync is installed and in use here: `/opt/homebrew/bin/claude-sync` v1.17.1,
  `~/.claude-sync/config.yaml`, R2, `scope: sessions`.
- codex-migrate (jsegeren) was evaluated and rejected for this need: one-time
  Mac-to-Mac move over SSH/rsync, replace-not-merge, Codex must be quit on both Macs;
  its own docs say "not continuous sync". Still the right tool for a one-off new-Mac move.
- Three options costed on 2026-09-15: adapt claude-sync (~200–400 lines of Go, days)
  < new project (1–3k lines, weeks) < edit codex-migrate (a rewrite disguised as an edit).
  Chosen: new standalone repo seeded from claude-sync with full history.
- Zero-code fallback if the Go work stalls: `rclone bisync` + `crypt` remote + launchd +
  `sqlite3 .backup` pre-step (no path mapping, no status/diff/conflicts UX).
- Research detail and inventories: `plan/notes.md`.

## Phases
- [x] Phase 0: Bootstrap repo (2026-09-15) — `d-jiao/codex-sync` (private); cloned from
  `tawanorg/claude-sync` main @ `49420ef`; `upstream` remote with `tagOpt --no-tags`;
  `2ef1c88` rename (module `github.com/d-jiao/codex-sync`, binary `codex-sync`,
  config `~/.codex-sync/`, env `CODEX_SYNC_*`); `f3cd042` strip npm/release/plugin/assets.
  - [ ] Loose ends: create `LICENSE` (MIT; `NOTICE` is staged and references it) and
    commit; remove the Node job from `.github/workflows/ci.yml` (~L60–67, it tested the
    deleted `install.js`); delete leftover `bin/claude-sync.js`; gitignore `bin/codex-sync`.
- [ ] Phase 1: Spike — copy `sessions/` (+ `session_index.jsonl`) into a fresh
  `CODEX_HOME`; does Codex list those conversations? Decides whether SQLite state must
  be synced at all. Output is an answer, not code.
- [ ] Phase 2: Design spec → `docs/specs/2026-09-15-codex-sync-design.md` (repo
  convention: `docs/specs/`). Cover: base dir `~/.codex`; Codex path profile + default
  excludes; bucket/namespace; `${HOME}` rewriting of `cwd` in rollout JSONL and
  `session_index.jsonl`; gate or remove Claude-only modules; SQLite policy (from spike);
  daily schedule (launchd); README/CLAUDE.md rewrite.
- [ ] Phase 3: Implementation plan from the spec (superpowers:writing-plans).
- [ ] Phase 4: Implement with TDD; `make check` green; upstream's test suite is the safety net.
- [ ] Phase 5: Roll out — new R2 bucket `codex-sync`; `codex-sync init` + first push on
  this Mac; pull on the second Mac; launchd daily job on both; verify a conversation
  resumes cross-machine.
- [ ] Phase 6: README + CLAUDE.md rewrite; flip repo public (MIT derivative); optional
  upstream PR adding the missing `LICENSE` file to claude-sync.

## Key Questions
1. Does Codex rebuild its session list from `sessions/**/rollout-*.jsonl`, or is
   `state_5.sqlite` / `thread_history_1.sqlite` authoritative? (Phase 1)
2. Do both Macs use the same macOS username and project layout? If yes, `cwd`
   rewriting can wait.
3. Sync `archived_sessions/` (387 MB here) in v1, or sessions-only first?
4. Which Codex writes are safe to sync while Codex runs (rollout JSONL is append-only?),
   and what needs a quiesce or snapshot?

## Decisions Made
- Standalone repo, not the contribution fork: GitHub allows one fork per account, and
  `d-jiao/claude-sync` (branch `feat/desktop-sidebar-sync`) stays reserved for upstream PRs.
- Keep upstream history, drop tags: engine fixes come over with `git cherry-pick -x`;
  versioning restarts at 0.1.0. Never `git merge upstream/main` (would pull Claude-specific work).
- Rename in one commit so both tools coexist on one machine; accepted cost: import-line
  conflicts on future cherry-picks (fix with one sed).
- Stay in Go: crypto, storage, state and conflict logic are done and tested; the
  remaining changes are plumbing.
- v1 uses a separate R2 bucket: claude-sync writes keys at the bucket root and has no
  configurable prefix.
- Behavior change (`~/.claude` → `~/.codex`) is deliberately NOT part of the bootstrap;
  it is Phases 2–4.
- Private repo until it works; public later.

## Errors Encountered
- Step-5 bootstrap block only echoed a placeholder for `LICENSE`, so the file was never
  created; `NOTICE` is staged, uncommitted. → Phase 0 loose ends.
- Step-4 block did not edit `ci.yml` (the manual edit was noted, not scripted); Node job
  still present and would fail CI on the missing `install.test.js`. → Phase 0 loose ends.

## Status
**Phase 0 nearly complete** — bootstrap pushed; finish loose ends, then the Phase 1 spike.

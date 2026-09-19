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
  - [x] Loose ends done in `6731c59`: LICENSE + NOTICE, CI Node job and semantic-release
    target removed, `.gitignore` renamed, `bin/claude-sync.js` deleted, plan files added.
- [x] Phase 1: Spike (2026-09-15) — **answer: rollout files are the source of truth;
  no SQLite needs to be synced.** A fresh `CODEX_HOME` holding only `sessions/`,
  `archived_sessions/` and `session_index.jsonl` rebuilds `state_5.sqlite` and lists the
  same user-visible threads as the real home (111 vs 111, archived 90); older rollouts
  dropped in after the DB exists are discovered immediately (no watermark gating).
  Gaps: thread names (DB-only at runtime, durable in `session_index.jsonl`) and
  `config.toml` provider dependence. Evidence in `plan/notes.md` → "Spike results".
- [x] Phase 2: Design spec → `docs/specs/2026-09-15-codex-sync-design.md` (repo
  convention: `docs/specs/`). Cover: base dir `~/.codex`; Codex path profile + default
  excludes; bucket/namespace; `${HOME}` rewriting of `cwd` in rollout JSONL and
  `session_index.jsonl`; gate or remove Claude-only modules; SQLite policy (from spike);
  daily schedule (launchd); README/CLAUDE.md rewrite.
- [x] Phase 3: Implementation plan — `plan/2026-09-15-codex-sync-v1-implementation.md` (12 tasks, TDD, one commit each).
- [x] Phase 4: Implement with TDD; `make check` green; upstream's test suite is the safety net.
- [x] Phase 5: Roll out (2026-09-15/16) — R2 bucket `codex`; `init` + push on this Mac
  (324 files); `init` + pull on Bob (backup kept; `rules/default.rules` and the pasted-text
  attachments index had to be unioned by hand, see follow-ups #10/#11); Bob pushed 1411,
  this Mac pulled 1410 + 1 merge, 0 conflicts. Engine check on an APFS clone: all 323 of
  Bob's user-visible threads listed (650 more rollouts are sub-agent threads, hidden by
  design); names do not carry over (§8 confirmed). No scheduler: the daily launchd agent
  was declined (2026-09-16) — sync stays manual, `codex-sync pull && codex-sync push`.
- [ ] Phase 6: README + CLAUDE.md rewrite; flip repo public (MIT derivative); optional
  upstream PR adding the missing `LICENSE` file to claude-sync.

## Key Questions
1. ~~Does Codex rebuild its session list from rollout files?~~ **Yes** (Phase 1 spike):
   `state_5.sqlite` and `thread_history_1.sqlite` are derived; never sync them.
2. ~~Do both Macs use the same macOS username?~~ Yes — but nothing is configured either
   way: the engine's automatic `${HOME}` mapping stays (a no-op for equal homes).
3. Sync `archived_sessions/` (387 MB here) in v1, or sessions-only first?
4. Which Codex writes are safe to sync while Codex runs (rollout JSONL is append-only?),
   and what needs a quiesce or snapshot? (No snapshot needed for SQLite any more.)
5. Names: `session_index.jsonl` is the durable record (111/112 names match), but the
   backfill never copies names into `threads.name`, so a pulled machine lists threads
   unnamed. Options: post-pull reconciliation of `threads.name` (tiny UPDATE, only with
   Codex closed), or document as a v1 limitation. Also: this file is single-file
   last-writer-wins across machines → needs a union-by-id merge like `history.jsonl`.
6. Does Codex refresh `updated_at`/preview when a synced rollout grows (pulled after the
   other Mac appended turns)? `thread/list` reported `updatedAt` = file mtime, which
   suggests yes; verify with a real append.
7. `config.toml`: threads are recorded per model provider and the listing shows only the
   *current* provider's threads. Both Macs need the same provider config (`cpa` here),
   so `config.toml` belongs in the sync set — but it contains absolute `[projects."…"]`
   paths (home-rewrite needed) and machine-specific sections; decide merge vs. copy.
8. Both Macs must run the same Codex engine version: 0.142.1 could not list threads
   written by 0.153.1/0.154 (rows silently dropped). Support caveat, not a sync bug.

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
- Sync set is files only: `sessions/`, `archived_sessions/`, `session_index.jsonl`,
  `history.jsonl`, `config.toml` (with care), `rules/`, `skills/`, `memories/`, `AGENTS.md`.
  Every `*.sqlite*` is derived or machine-local and is excluded (spike, 2026-09-15).
- No scheduled sync (2026-09-16): the user runs `codex-sync pull && codex-sync push` by
  hand on each Mac; `make install-launchd` remains available but is not used.

## Errors Encountered
- Step-5 bootstrap block only echoed a placeholder for `LICENSE`, so the file was never
  created; `NOTICE` is staged, uncommitted. → Phase 0 loose ends.
- Step-4 block did not edit `ci.yml` (the manual edit was noted, not scripted); Node job
  still present and would fail CI on the missing `install.test.js`. → Phase 0 loose ends.

- Spike gotchas: (a) `thread/list` filters by current model provider unless
  `modelProviders` is passed; (b) engine version skew hides threads; (c) the TUI picker
  can't be captured with `script` — it waits on terminal capability queries.
- Rollout: R2 `HeadBucket` 403 when the API token is scoped to another bucket; the
  upload stamp bug (R2 `LastModified` lands up to ~20 ms after the local clock) made the
  first pull want 179 re-downloads — fixed in 3829223; a faulty BSD `sed` printed the R2
  secret into the transcript → rotate the token.

## Status
**Phase 5 done** — manual sync on both Macs (no launchd by decision); desktop refresh is now `codex-sync desktop refresh` / `pull --desktop`; run it on Bob after `git pull && make install`. Next: rotate the R2 token that leaked into the transcript (`init --force` with the new keys on both), then Phase 6.

## Follow-ups after the whole-branch review (2026-09-15)

Ordered by importance; none block the Phase 5 rollout, but #1 should land before relying on
conflict resolution across machines.

1. ~~`conflicts --keep local` never re-pushed the kept version~~ — fixed (`keepLocal` records
   the remote's hash from the sidecar; spec §7's "the next push publishes the kept version"
   now holds).
2. Sidecars accumulate: every pull on a machine with an unresolved conflict writes another
   `<path>.conflict.<ts>` (the original path's state is not advanced). Pre-existing; a
   one-sidecar-per-conflict rule would keep the daily job tidy.
3. Mass-removal guard: refuse (or require `--force`) when a pull would trash more than ~20%
   of tracked files, so a wiped home on one machine cannot empty the other via the daily job.
4. `findConflicts` walks the whole base dir including hard-excluded trees (`worktrees/`,
   `packages/`); skip `config.IsHardExcluded` directories.
5. `internal/sync/sync.go` is ~1100 lines (limit 800; 970 at the fork): pure-move split of
   Codex-only helpers (`staleLocalFiles`/`moveToTrash` → `trash.go`, `mergeRemote` → `merge.go`,
   `PreviewPull` → `preview.go`) without touching upstream-shared bodies (keeps cherry-picks clean).
6. Preview summary omits merge/remove counts; `PreviewPull` drops path_map-unresolvable keys
   that `Pull` reports; `downloadManifest` should use `fetchRemote` (keep soft-fail).
7. Tests: `moveToTrash` copy fallback (inject the rename), same-home byte-identical `${HOME}`
   round trip, `PreviewPull` on an empty remote, an upload-side `IsProtected` guard.
8. `integration/r2_sync_test.go` (build-tag gated, real R2) still uses Claude-profile fixtures.
9. Split `cmd/codex-sync/main.go` (~3000 lines) per command — unrelated churn, do it separately.
10. `attachments/pasted-text-attachments.json` is a shared-append index (`attachmentPaths`,
    `pendingRemovalPaths`, `textExcerptsByPath`): Bob's first pull replaced its 5-entry
    index with this Mac's empty one and it had to be unioned by hand (2026-09-15). Add it
    to `IsMergeablePath` with a per-field union (lists: ordered union; excerpts: key union),
    after checking how the ChatGPT app treats entries whose file is gone.
11. `rules/default.rules` likewise: the two machines had disjoint `prefix_rule` lines and
    the pull kept only the remote's. Codex appends one rule per line, so a line-set union
    is the natural merge — same treatment as #10.
12. `push` has no `--dry-run` (`status` is the preview); add it for symmetry with `pull`.
13. `codex-sync diff` compares plaintext local size with the compressed remote size, so it
    reports every file as modified; compare manifest hashes instead (or drop the size).
14. Wizard prompt says "Passphrase (min 8 chars)" but validation requires 12.
15. `reconcile_names` (spec §8), now with evidence (2026-09-16): neither the backfill of new
    rollouts into an existing `state_5.sqlite` nor a from-scratch rebuild reads names from
    `session_index.jsonl` (rebuilt clone: 0 of this Mac's 114 names survived), so the index
    is write-only for Codex and the opt-in DB write is the only way to carry names across
    machines. 309 of Bob's 323 threads are named — this is the most visible v1 gap.
16. Desktop-app visibility (2026-09-16, spec §13 answered): the ChatGPT app's catalog never
    shows pulled threads on its own (one full build, then watermark-gated incremental
    scans) and its engine only indexes rollouts it discovers itself. Manual remedy shipped
    as `scripts/codex-desktop-refresh.py` (backup → engine index run → names from
    `session_index.jsonl` into `threads.name` → clear `last_full_reconciled_at`); verified
    here: catalog 62 → 143 threads, all named. ~~Follow-up: fold it into `codex-sync pull`~~
    Done 2026-09-19: `internal/desktop` + `codex-sync desktop refresh` / `pull --desktop`
    (script removed).

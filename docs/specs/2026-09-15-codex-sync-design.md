# codex-sync v1 — design

Status: implemented (v1; §8 names and §13 desktop visibility were later resolved by `desktop refresh`) · Written: 2026-09-15 · Supersedes nothing (first spec of the fork)

## 1. Purpose

Sync the local state of OpenAI Codex (`$CODEX_HOME`, default `~/.codex`) between a
user's machines through encrypted object storage, on a daily schedule and on demand,
so that conversations started on one machine can be listed and resumed on another.

codex-sync is a fork of claude-sync. The engine — storage adapters, age encryption,
per-file state tracking, push/pull, conflict sidecars, `${HOME}` path portability,
`path_map`, the CLI shape and the setup wizard — is reused unchanged unless a section
below says otherwise. This spec describes only what differs for Codex.

### Non-goals (v1)

- Real-time or event-driven sync; the model is push/pull with a scheduler on top.
- Merging any SQLite state (project organization, automations, memories, app catalog).
- `~/.agents/skills` (outside the base dir), `worktrees/` (checked-out repositories),
  plugins, packages, caches, logs.
- Windows and Linux, npm distribution, a Claude Code plugin wrapper.
- Reconciling thread names into Codex's database (see §8; deferred, opt-in later).

### Evidence base

A spike run before this spec (fresh `CODEX_HOME` directories seeded with copies of a
real home's files but none of its databases, listed non-interactively through the
engine's `app-server` protocol and compared against the full home): rollout JSONL files
are the source of truth; a fresh `CODEX_HOME` holding only `sessions/`,
`archived_sessions/` and `session_index.jsonl` rebuilds `state_5.sqlite` and lists the
same user-visible threads as the original home; rollouts added later (even older ones)
are discovered on the next listing; every `*.sqlite*` file is derived or machine-local.
`integration/codex_listing_check.py` is the reusable form of that comparison.

## 2. Assumptions and constraints

- All machines run the same Codex engine version. Version skew hides threads written by
  a newer engine (spike). This is a support caveat, not something codex-sync detects in v1.
- Nothing about usernames or hostnames is configured. `${HOME}` is derived on each
  machine from the OS; identical homes make path rewriting a byte-identical no-op,
  different homes are rewritten automatically (existing engine behavior). Machine-specific
  facts live only in `~/.codex-sync/config.yaml`, never in the repository.
- A thread should not be actively resumed on two machines between syncs; if it is, the
  engine's conflict rule keeps the local file and saves the remote as a sidecar (§7).
- Remote storage is a bucket dedicated to codex-sync (keys sit at the bucket root with no
  prefix support), separate from any claude-sync bucket.

## 3. Storage and crypto (unchanged, with one note)

Remote key = path relative to the base dir with `.age` appended, home-tokenized; content
is gzip-compressed then age-encrypted; passphrase keys derive with Argon2id and a fixed
salt. The salt is `sha256("codex-sync-v1")` — deliberately different from claude-sync's
`claude-sync-v1` so the same passphrase yields different keys for the two tools. Like the
original, it must never change again.

## 4. Base directory

`config.BaseDir()` returns `$CODEX_HOME` when set (Codex's own override), otherwise
`~/.codex`. Every former "claude dir" reference uses it. The first-pull backup directory
becomes `<base>.backup.<timestamp>`. Tool state stays in `~/.codex-sync/`
(`config.yaml`, `age-key.txt`, `state.json`).

## 5. Sync set — the Codex profile

Include-list semantics as in claude-sync (`SyncPaths` / `SessionSyncPaths`, with
`sync_paths` and `exclude` in config, scope as a ceiling). Default scope: `full` — the
Codex full set is small because caches and plugins are never included.

| Path (relative to base) | Scope | Content rewrite | Merge on conflict | Notes |
|---|---|---|---|---|
| `sessions/` | sessions | yes (`.jsonl`) | sidecar | rollouts; source of truth |
| `archived_sessions/` | sessions | yes | sidecar | `codex archive` moves files here |
| `session_index.jsonl` | sessions | yes | union by `id` (§6) | thread names |
| `history.jsonl` | sessions | yes | union by line (§6) | prompt history |
| `attachments/` | sessions | no | sidecar | user-provided files referenced by threads |
| `config.toml` | full | yes (`.toml`) | sidecar | providers, MCP servers, project trust with absolute paths |
| `rules/` | full | yes (`.md/.txt/.json`) | sidecar | |
| `skills/` | full | yes (`.md/.txt/.json`) | sidecar | scripts are copied verbatim |
| `memories/` | full | yes | sidecar | directory only; the `.sqlite` beside it is excluded |
| `AGENTS.md` | full | yes | sidecar | global instructions |

Hard excludes, applied even when a user lists a parent directory in `sync_paths`:
`auth.json`, `installation_id`, `*.sqlite`, `*.sqlite-wal`, `*.sqlite-shm`, `*.db`,
`*.db-wal`, `*.db-shm`, `sqlite/`, `logs*`, `.codex-global-state.json*`,
`..codex-global-state.json*`, `plugins/`, `packages/`, `cache/`, `.tmp/`, `tmp/`, `ipc/`,
`thread-writer-locks/`, `shell_snapshots/`, `models_cache.json`, `computer-use/`,
`vendor_imports/`, `browser/`, `node_repl/`, `process_manager/`, `dictation-history/`,
`transcription-history.jsonl`, `version.json`, `worktrees/`, `*.bak`, `*.tmp-*`.
`auth.json` and `installation_id` are additionally *protected*: they are never uploaded
and never written by pull, regardless of configuration.

Files Codex regenerates in an empty home (spike) are therefore never synced:
`installation_id`, `state_5.sqlite*`, `logs_2.sqlite*`, `goals_1.sqlite*`,
`memories_1.sqlite*`, `queue_1.sqlite*`, `skills/` (created empty), `.tmp/`, `tmp/`.

## 6. Portability and merge rules

Content rewriting (`IsPortableContentPath`) becomes: true for `history.jsonl`,
`session_index.jsonl`, `config.toml`, `AGENTS.md`, and for `.jsonl/.json/.md/.txt/.toml`
files under `sessions/`, `archived_sessions/`, `rules/`, `skills/`, `memories/`.
Conflict sidecars inherit the rule of their base path (existing behavior). Remote-key
tokenization stays but is a no-op: rollout names carry dates and UUIDs, not paths.

Two shared single files are merged instead of producing sidecars, because last-writer-wins
silently loses entries when two machines push:

- `session_index.jsonl` — lines `{id, thread_name, updated_at}`; union by `id`, keep the
  entry with the latest `updated_at`; output sorted by `updated_at`.
- `history.jsonl` — union of distinct lines, ordered by their `ts` field; lines without a
  parsable `ts` sort first, in their original order.

Merge happens on pull whenever the remote file differs from the state hash (not only in
the both-changed case), the merged result is written locally and recorded in state so
the next push uploads the union. Lines that fail to parse are kept verbatim at the end.
The merge is idempotent. `rebuild-history` (rebuilt Claude's history from project
transcripts) is removed; a rollout-based rebuild can be added later if needed.

## 7. Deletions, moves, conflicts

- Push: files removed locally since the last sync are deleted remotely. A modified
  file that still has a live `<path>.conflict.*` sidecar next to it is **not**
  uploaded: push reports `unresolved conflict for <path>; run 'codex-sync conflicts'`
  and fails its exit code, while every other file still uploads. Resolving the
  conflict removes the sidecar, and the next push publishes the kept version.
- Pull (new): a file recorded in state but absent from the remote listing is removed
  locally **only if** its current hash equals the state hash (unchanged since the last
  sync); otherwise it is kept in place and listed in the pull summary (no sidecar).
  Removed files are moved to
  `~/.codex-sync/trash/<timestamp>/<relative path>` rather than unlinked. `pull --no-delete`
  opts out; `pull --dry-run` lists planned removals. Rationale: `codex archive` moves a
  rollout between `sessions/` and `archived_sessions/`; without pull-side removal the
  other machine holds the same thread twice.
- Conflicts: unchanged — local kept, remote saved as `<path>.conflict.<timestamp>`,
  resolved with `codex-sync conflicts`. Sidecar names do not end in `.jsonl`, so Codex's
  rollout scanner is expected to ignore them (verify in Phase 5, §12).
- Sidecars are local artifacts only: `*.conflict.*` is a hard exclude, so they are never
  uploaded, never tracked in state (the pull that writes one drops its entry), and never
  moved to the trash as a "vanished remote file"; they exist until `codex-sync conflicts`
  resolves them.

## 8. Thread names

`session_index.jsonl` is the durable record of names and is synced (§5, §6). Codex's
backfill does not copy names from it into `threads.name` in `state_5.sqlite`, so a thread
pulled onto another machine appears unnamed there until renamed. v1 documents this as a
limitation. A later opt-in (`reconcile_names: true`) may update `threads.name` from the
index after a pull, only when no Codex process is running and only if the table and
column exist; it is out of scope for v1.

## 9. Running alongside Codex

Rollouts are append-only. The engine overwrites a local file only when it is unchanged
since the last sync, so a thread being appended locally is never overwritten — it becomes
a conflict instead. The CLI engine indexes pulled rollouts on its next `thread/list`
(spike). The desktop app does not: its catalog (`sqlite/codex-dev.db`) is built in full
once and then scanned incrementally past an `updated_at` watermark, so pulled threads —
always older than the watermark — never appear until the full sweep is re-run
(verified 2026-09-16, see §13). `codex-sync desktop refresh` (also `pull --desktop`) does
that with the app quit: index via the app's engine, copy names from `session_index.jsonl`
into `threads.name` (§8), clear `last_full_reconciled_at`. Run it after any pull that
brought new threads.

## 10. CLI surface

- Kept: `init`, `push`, `pull`, `status`, `diff`, `conflicts`, `reset`.
- Changed: `pull` gains `--no-delete`; `init` shows the base dir and defaults scope to
  `full`; `update` and `changelog` point at this repository's releases and print a
  build-from-source hint until releases exist.
- Removed: `migrate` (legacy claude-sync key layout), `rebuild-history` (§6), `mcp` and
  the `mcp_sync` / `--include-mcp` options (Codex MCP servers live in `config.toml`, which
  is synced as a file).
- Scheduling: `scripts/launchd/com.codex-sync.daily.plist.template` (daily `pull` then `push`, quiet
  output) and `make install-launchd` / `make uninstall-launchd`; no scheduler code in Go.

## 11. Module changes

| Area | Change |
|---|---|
| `internal/config` | `BaseDir()`/`BaseDirE()` honoring `CODEX_HOME`; Codex `SyncPaths`/`SessionSyncPaths`; `HardExcludes` and `ProtectedPaths`; drop MCP fields |
| `internal/sync/paths.go` | portability rules per §6 |
| `internal/sync/history.go` | replaced by `merge.go`: JSONL union merges per §6 |
| `internal/sync/mcp.go`, `internal/claudesettings/` | deleted; `_external/` filtering may remain as a harmless guard |
| `internal/sync/sync.go` | pull-side removal with trash (§7); merge hook (§6); protected-path guard |
| `cmd/codex-sync/main.go` | command surface (§10), user-facing strings, backup dir, first-pull flow against the base dir |
| `integration/` | `codex_listing_check.py`: generic acceptance script derived from the spike harness — synced home must list the same user-visible threads as the source home (manual; needs a Codex engine binary) |
| Docs | README and CLAUDE.md rewritten for Codex; SECURITY-AUDIT gains the salt note (§3) |

Error handling follows the engine: every network or decrypt failure aborts the run
without touching state; partial pulls are safe to rerun; the merge and trash steps are
atomic per file (write temp, rename).

## 12. Rollout and acceptance (Phase 5)

1. Machine A: `codex-sync init` (new bucket, passphrase), `codex-sync push`.
2. Machine B: `codex-sync init` with the same passphrase, `pull --dry-run`, `pull`.
3. On B, open Codex: pulled threads are listed and one resumes; run
   `integration/codex_listing_check.py` against both homes.
4. Append on A (continue a thread), push; pull on B; the thread shows the new turns.
5. `codex archive` on A, push; pull on B; B holds the thread once, under
   `archived_sessions/`; the original is in `~/.codex-sync/trash/`.
6. Rename a thread on A, push, pull on B: `session_index.jsonl` carries the name; confirm
   the documented limitation in the app (§8).
7. Install the launchd job on both; confirm the next scheduled run completes.

## 13. Open questions

- ~~Does the desktop app's catalog pick up pulled threads without a restart, and how fast?~~
  Never (2026-09-16): `isFullReconciliationDue` is true only until the first full build;
  afterwards scans stop at the `updated_at` watermark. Resolved by
  `codex-sync desktop refresh` and `pull --desktop` (§9).
- Do `*.conflict.*` sidecars inside `sessions/` stay invisible to Codex?
- Does Codex refresh `updated_at`/preview when a synced rollout grows? (`thread/list`
  reported the file's mtime, which suggests yes.)
- `attachments/` size in general use — small here; revisit the scope placement if large.

## 14. Testing

- Unit: profile lists and scope ceiling; hard excludes and protected paths; portability
  rules including `.toml`; `${HOME}` round trip is byte-identical when homes match; merges
  are idempotent, keep latest-by-`updated_at`, preserve unparsable lines; pull-side removal
  respects the unchanged-only rule and writes to trash; `--no-delete`.
- Existing suite stays green; CI's 60% coverage floor on `internal/*` holds.
- Acceptance per §12, with a script that takes both homes as arguments (nothing
  machine-specific is committed).

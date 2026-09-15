# Notes: codex-sync research (2026-09-15)

## Sources

### Source 1: codex-migrate (jsegeren/codex-migrate; local checkout `../codex-migrate`, fork `d-jiao/codex-migrate`)
- URL: https://github.com/jsegeren/codex-migrate — https://migrate.segeren.com
- Key points:
  - One-time Mac-to-Mac migration: inspect → stage (`rsync --partial` over SSH) →
    finalize (verified backup, then replace) → verify (SHA-256 trees, SQLite integrity,
    destination auth preserved). Python, ~8.5k LOC, plus commerce/site/desktop cruft.
  - Replace-not-merge; Codex must be quit on both Macs; peer-to-peer, no cloud, no
    encryption at rest; no incremental state; CLI: launch/inventory/serve/inspect/export/recovery.
  - `docs/maintainer-handoff.md`: "a one-time migration tool, not continuous sync or a
    merge engine"; "continuous cross-device sync are not supported".
  - Reusable knowledge only: which `~/.codex` entries are identity/runtime (never copy
    `auth.json`, `installation_id`, sockets, locks, logs, caches); personal skills live in
    `~/.agents/skills` (legacy `~/.codex/skills`); managed worktrees in `~/.codex/worktrees`.
  - Its docs cite a Reddit thread "made a tool to sync codex chats and configs"
    (r/codex, id 1tqczl5) — unverified prior art; check before over-building.

### Source 2: claude-sync (tawanorg/claude-sync) — the base of this repo
- Go, ~8.8k LOC. R2/S3/GCS/S3-compatible/WebDAV; gzip → age (Argon2 passphrase KDF);
  push/pull/status/diff/conflicts; `${HOME}` path token + `path_map`; excludes;
  self-update; wizard; tests, `make check`, `.githooks` pre-commit.
- Internals map (line numbers as of upstream `49420ef`; strings renamed in `2ef1c88`):
  - `internal/config/config.go`: `ClaudeDirE()` hardcodes `~/.claude` (~L155);
    `SyncPaths` / `SessionSyncPaths` (~L85–108); `ExternalKeyPrefix = "_external/"` (L24)
    for objects outside the base dir; `PathMap`.
  - `internal/paths/manager.go`: `NewManager(syncPaths, excludes, claudeDir, scope)` —
    the engine already takes the base dir as a parameter.
  - `internal/sync/paths.go`: `${HOME}` token rewriting of keys and content;
    `history.jsonl` special case (~L203).
  - Claude-only, to gate or remove: `internal/sync/history.go` (rebuilds `history.jsonl`
    from `projects/*.jsonl`), `internal/sync/mcp.go` (merges `~/.claude.json` MCP servers
    via `_external/`), `internal/claudesettings/`.
  - Remote keys = paths relative to the base dir, at bucket root; no configurable prefix.
  - CI (`.github/workflows/ci.yml`): go test + coverage, go build, golangci-lint
    (not installed locally; `make check` = gofmt/vet/`go test -short`, no lint), and a
    Node job for `install.js` (to delete).
  - Repo conventions: specs in `docs/specs/` (existing: `openclaw-bifrost-sync.md`);
    `CLAUDE.md` is developer guidance and still describes claude-sync (mentions the
    removed npm wrapper `bin/claude-sync.js`) — rewrite in Phase 6.

### Source 3: `~/.codex` inventory on this Mac (2026-09-15)
Sync candidates (analog of claude-sync's `sessions` scope):

| Path | Size | Notes |
|---|---|---|
| `sessions/` | 219 MB, 143 files | `sessions/YYYY/MM/DD/rollout-<ts>-<uuid>.jsonl` — keyed by date + UUID, no project path in the key |
| `archived_sessions/` | 387 MB | archived threads |
| `session_index.jsonl` | 15 KB | thread index (check for `cwd`/titles) |
| `history.jsonl` | tiny | prompt history; single file → last-writer-wins like claude-sync |
| `config.toml` | 4 KB, mode 600 | user config |
| `rules/`, `skills/` (912 KB), `memories/`, `AGENTS.md` | small | config-like |
| `~/.agents/skills/` | empty here | documented current personal-skills location |

Exclude (identity, runtime, caches, machine-specific): `auth.json`, `installation_id`,
`logs_2.sqlite` (440 MB), `plugins/` (320 MB), `packages/` (242 MB), `cache/`, `.tmp/`,
`tmp/`, `ipc/`, `thread-writer-locks/`, `shell_snapshots/`, `.codex-global-state.json*`
and the `..codex-global-state.json.tmp-*` leftovers, `models_cache.json`, `computer-use/`
(70 MB), `vendor_imports/`, `browser/`, `node_repl/`, `process_manager/`,
`dictation-history/`, `transcription-history.jsonl`, `version.json`, `*.sqlite-wal`,
`*.sqlite-shm`.

Undecided — live WAL-mode SQLite (cannot be copied mid-write; snapshot with
`sqlite3 <db> ".backup <copy>"` if needed; never file-mergeable across machines):
`state_5.sqlite` (7 MB), `thread_history_1.sqlite` (112 MB), `memories_1.sqlite`,
`goals_1.sqlite`, `queue_1.sqlite`, `sqlite/` (84 MB). The Phase 1 spike decides.

Also undecided: `attachments/`, `visualizations/`, `ambient-suggestions/`, `rollout-migrations/`.

## Synthesized Findings

### Why claude-sync's model fits Codex
- Codex session keys are already portable (date + UUID), so claude-sync's remote-key
  rewriting becomes a no-op; only the `cwd` field inside rollout JSONL /
  `session_index.jsonl` needs `${HOME}` rewriting, and only if usernames differ.
- Rollout files are one-per-session like Claude's, so conflicts are rare and per-file.

### Cost comparison (2026-09-15)
| Option | Reuse | Must build | Size |
|---|---|---|---|
| Adapt claude-sync | storage, crypto, state, conflicts, path map, CLI, tests, CI, existing bucket/key | base-dir config, Codex path profile, gate 3 Claude-only modules, bucket/namespace, SQLite snapshot | ~200–400 lines Go, days |
| New project | nothing | everything above | 1–3k lines, weeks |
| Edit codex-migrate | ~100 lines of exclusion constants | transport, encryption, state, conflicts; strip replace-with-Codex-closed semantics across 8.5k LOC | a rewrite |

### Upstream-sync recipe (engine fixes only)
```bash
git fetch upstream
git log --oneline --no-merges main..upstream/main -- internal cmd
git cherry-pick -x <sha>
# on import-line conflicts:
# sed -i '' 's#github.com/tawanorg/claude-sync#github.com/d-jiao/codex-sync#g' <files>
```

### Zero-code fallback
`rclone bisync ~/.codex r2crypt:codex --filter-from codex.filter`, a `sqlite3 .backup`
pre-step, and a launchd plist. Loses path mapping and the status/diff/conflicts UX.

## Spike results (2026-09-15): where Codex keeps thread state

**Question.** Can a machine that receives only files (no SQLite) list and resume the
other machine's Codex threads?

**Method.** Fresh `CODEX_HOME` directories in the scratchpad (APFS clones of
`~/.codex/sessions`, later `archived_sessions/`, plus `session_index.jsonl`; no DBs, no
`auth.json`, no `config.toml`). Threads listed non-interactively over the app-server
protocol (`codex app-server --stdio`, `initialize` → `thread/list`, paginated, all
`sourceKinds`, `modelProviders: ["cpa","openai"]`). Engine: the ChatGPT app's bundled
`codex-cli 0.154.0-alpha.6.2` (`/Applications/ChatGPT.app/Contents/Resources/codex`).
Control: a full clone of the real home with its real DB. Harness kept as throwaway in
`plan/spike/list_threads.py`.

**Findings.**
- `state_5.sqlite` (sqlx, 54 migrations) has `threads` (id, **absolute** `rollout_path`,
  cwd, title/name, archived, model_provider, …), `backfill_state` (one-time scan,
  `complete`), `rollout_migration_state`, `thread_spawn_edges`, `projects`, and
  `remote_control_enrollments` (identity — never sync). `thread_history_1.sqlite` is a
  projection of rollouts with per-thread byte offsets. `sqlite/codex-dev.db` is the desktop
  app's catalog (`local_thread_catalog`, hosts `local` + `chatgpt:<workspace>`) plus
  `automations` (empty here). `memories_1.sqlite` empty (feature experimental).
- Fresh home, files only: backfill created `state_5.sqlite` with 139 threads = the 139
  rollouts present; listing = 109 user-visible threads vs control 111 (difference = the
  withheld day). All unlisted files are `thread_spawn` sub-agent children (37 in control).
- Older rollouts added after the DB existed (simulated pull): indexed and listed on the
  next `thread/list` (109 → 111, DB 139 → 141). No watermark gating.
- `archived_sessions/` added later: 90 archived threads listed with `archived: true`.
- Names: fresh home lists 0 named threads vs 55 in control. `session_index.jsonl`
  (`{id, thread_name, updated_at}`) matches 111/112 DB names exactly; names are not inside
  rollout files. So the file is durable, the DB column is a runtime cache the backfill
  doesn't fill.
- Files the engine regenerates in an empty home (never sync): `installation_id`,
  `state_5.sqlite*`, `logs_2.sqlite*`, `goals_1.sqlite*`, `memories_1.sqlite*`,
  `queue_1.sqlite*`, `skills/`, `.tmp/`, `tmp/` (0.142 also wrote a minimal `config.toml`).
- Gotchas: default `thread/list` shows only the current model provider's threads (fresh
  home = `openai` → 33 of 139); the standalone CLI 0.142.1 listed 2 threads from a DB
  written by 0.153.1 (schema/JSON skew) — keep engine versions aligned on both Macs;
  `thread/list.updatedAt` followed the copied file's mtime (append re-index likely, unverified).

**Recommendation.** Sync files only; exclude every `*.sqlite*`. Add a post-pull
name-reconciliation step or document names as a v1 gap. Treat `session_index.jsonl` like
`history.jsonl` (union merge by id). Put `config.toml` in scope with home-path rewriting
for `[projects."…"]` keys. Test the append case during Phase 4 with a real second machine.

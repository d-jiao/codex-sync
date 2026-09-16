# Changelog

## [Unreleased]

### Added

- Fork from tawanorg/claude-sync; see NOTICE.
- Codex base directory resolution via `$CODEX_HOME` (falls back to `~/.codex`).
- Codex sync profile (`SyncPaths` / `SessionSyncPaths`) with hard excludes and protected paths for `auth.json` and `installation_id`.
- Merge-on-pull for `session_index.jsonl` and `history.jsonl`: the remote and local copies are unioned instead of one overwriting the other.
- Pull-side removal: local files that vanished from the remote are moved to `~/.codex-sync/trash/<timestamp>/` instead of going stale; `pull --no-delete` opts out.
- `make install-launchd` / `make uninstall-launchd` targets and a daily launchd agent template (`scripts/launchd/com.codex-sync.daily.plist.template`); `CODEX_HOME` is baked into the agent when set at install time.
- Push skips a file that still has a live `.conflict.*` sidecar and reports `unresolved conflict for <path>`; sidecars are hard-excluded and never uploaded, tracked or trashed.
- `push`/`pull` print errors to stderr even with `-q` and exit non-zero when any file failed.

### Changed

- `reset` removes `config.yaml`, `age-key.txt` and `state.json` individually and leaves `~/.codex-sync/trash/` intact.
- Merge tie-breaks (same `id` and `updated_at`; equal history `ts`) use the raw line bytes, never arrival order.
- The `logs*` hard exclude is gone (`logs/` and `*.sqlite*` still cover the real targets); `*.conflict.*` is hard-excluded.

### Fixed

- Push records the remote's own timestamp for each uploaded file when it is later
  than the local clock (R2 stamps objects a few milliseconds after the upload
  returns), so the next pull no longer re-downloads files this machine just pushed.
- `conflicts --keep local` now records the remote's hash instead of marking the kept
  file as uploaded, so the next push publishes the kept version (previously it was never
  pushed and the other machine kept its own copy).

### Removed

- `mcp` command and the `mcp_sync` / `--include-mcp` options — Codex MCP servers live in `config.toml`, which syncs as a plain file.
- `migrate` — was claude-sync's legacy remote-key-layout converter; not applicable to a new fork.
- `rebuild-history` — superseded by merge-on-pull for `history.jsonl` (see Added).
- No built-in `auto` scheduler command — automatic sync runs through the launchd agent (`make install-launchd`) instead.

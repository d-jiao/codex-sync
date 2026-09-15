# Changelog

## [Unreleased]

### Added

- Fork from tawanorg/claude-sync; see NOTICE.
- Codex base directory resolution via `$CODEX_HOME` (falls back to `~/.codex`).
- Codex sync profile (`SyncPaths` / `SessionSyncPaths`) with hard excludes and protected paths for `auth.json` and `installation_id`.
- Merge-on-pull for `session_index.jsonl` and `history.jsonl`: the remote and local copies are unioned instead of one overwriting the other.
- Pull-side removal: local files that vanished from the remote are moved to `~/.codex-sync/trash/<timestamp>/` instead of going stale; `pull --no-delete` opts out.
- `make install-launchd` / `make uninstall-launchd` targets and a daily launchd agent template (`scripts/launchd/com.codex-sync.daily.plist.template`).

### Removed

- `mcp` command and the `mcp_sync` / `--include-mcp` options — Codex MCP servers live in `config.toml`, which syncs as a plain file.
- `migrate` — was claude-sync's legacy remote-key-layout converter; not applicable to a new fork.
- `rebuild-history` — superseded by merge-on-pull for `history.jsonl` (see Added).
- No built-in `auto` scheduler command — automatic sync runs through the launchd agent (`make install-launchd`) instead.

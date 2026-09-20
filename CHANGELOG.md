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
- `codex-sync desktop refresh` and `pull --desktop`: make pulled threads visible in the ChatGPT desktop app (engine re-index, names from `session_index.jsonl`, full catalog sweep on next launch), with a database backup first; refuses while the app is running.
- `push --force`: delete the remote copies of files deleted locally. Without it, push lists them and leaves them in storage.
- Remote recycle bin: `push --force` copies every object it deletes to `_trash/<batch>/` in the bucket before removing it, and abandons a delete whose copy cannot be written. Copies are the stored ciphertext and are excluded from every listing pull works from, so they are never downloaded as files and a bucket holding only copies still counts as empty.
- `codex-sync trash list` and `codex-sync trash restore <batch>`: find the copies a forced push kept and put them back under their original keys. A key that is live again is left alone and reported. Nothing prunes the bin automatically; use a lifecycle rule on the `_trash/` prefix or delete batches yourself.
- Per-file remote version tracking (`remote_version` in `state.json`): the provider's ETag or generation decides whether a remote object changed, falling back to timestamps when a provider does not supply one. Old state files load unchanged.

### Changed

- Push no longer deletes remote objects by default. A stale checkout, a restored backup or a half-configured `sync_paths` can no longer erase the copy other machines pull from; `push --force` opts in.
- `push --force` refuses to delete an object another machine replaced since the last sync (reported as a conflict) and, where the provider supports it, issues the delete conditionally on the object's version. Each object's version is re-read immediately before its delete rather than trusted from the listing taken at the start of the batch, so the guard holds on S3-compatible servers that accept `If-Match` on a DELETE and ignore it. Deletes the provider reported no version for are listed after the push as guarded by timestamps only.
- Pull writes every file atomically through a temporary file and refuses any path that crosses a symlink, instead of following it out of the Codex home. Conflict sidecars get a numeric suffix rather than overwriting an existing sidecar from the same second.
- Push re-checks a file's size and modification time around the upload and leaves it unsynced if Codex appended to it mid-upload, so partial content is never recorded as synchronized.
- A manifest that is listed remotely but cannot be downloaded, decrypted or parsed now fails the pull before anything is written; a missing manifest is still treated as a legacy remote. A failed manifest upload no longer discards the push: the state records `manifest_dirty` and the next push retries it.
- WebDAV percent-encodes each path segment of an object key, so names containing spaces, `#`, `?` or `%` work; PROPFIND hrefs are decoded segment by segment.
- `desktop refresh` finds running Codex processes by executable name via `ps` instead of matching a `pgrep` command-line pattern, so an app bundle under a path containing spaces is detected and a shell that merely mentions the engine path is not.
- The first-pull backup applies the configured excludes, keeping `auth.json`, SQLite databases and excluded paths out of `~/.codex.backup.<timestamp>`.
- `reset` removes `config.yaml`, `age-key.txt` and `state.json` individually and leaves `~/.codex-sync/trash/` intact.
- Merge tie-breaks (same `id` and `updated_at`; equal history `ts`) use the raw line bytes, never arrival order.
- The `logs*` hard exclude is gone (`logs/` and `*.sqlite*` still cover the real targets); `*.conflict.*` is hard-excluded.

### Fixed

- The `init` passphrase prompt said "min 8 chars" while validation requires 12.
- Push records the remote's own timestamp for each uploaded file when it is later
  than the local clock (R2 stamps objects a few milliseconds after the upload
  returns), so the next pull no longer re-downloads files this machine just pushed.
- `conflicts --keep local` now records the remote's hash instead of marking the kept
  file as uploaded, so the next push publishes the kept version (previously it was never
  pushed and the other machine kept its own copy).

### Removed

- The inherited claude-sync documentation pages under `docs/` (`index`, `architecture`,
  `how-it-works`, `security`), the unrelated OpenClaw spec, and `scripts/publish-npm.sh`;
  `docs/security.md` is now a codex-sync security model, and `CONTRIBUTING.md` /
  `SECURITY.md` are new.
- `mcp` command and the `mcp_sync` / `--include-mcp` options — Codex MCP servers live in `config.toml`, which syncs as a plain file.
- `migrate` — was claude-sync's legacy remote-key-layout converter; not applicable to a new fork.
- `rebuild-history` — superseded by merge-on-pull for `history.jsonl` (see Added).
- No built-in `auto` scheduler command — automatic sync runs through the launchd agent (`make install-launchd`) instead.

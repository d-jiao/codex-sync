# CLAUDE.md

This file provides build, test, and architecture guidance for engineers and
coding agents working in this repository.

## Repository Purpose

`codex-sync` is a Go CLI that syncs the OpenAI Codex home directory
(`$CODEX_HOME`, default `~/.codex`: sessions, archived sessions, thread names,
prompt history, attachments, `config.toml`, rules, skills, memories,
`AGENTS.md`) across devices via encrypted cloud storage (Cloudflare R2 / AWS
S3 / GCS / any S3-compatible endpoint / WebDAV). Files are gzip-compressed,
then age-encrypted before upload. It is a fork of claude-sync (see NOTICE);
the storage, crypto and per-file state engine is reused largely unchanged,
but the sync profile, merge rules and CLI surface are Codex-specific — see
`docs/specs/2026-09-15-codex-sync-design.md`. Distributed as source only: no
npm package and no pre-built binaries yet.

## Common Commands

```bash
make build              # build ./bin/codex-sync for the host platform
make install            # build + install to ~/.local/bin/codex-sync (or $INSTALL_DIR)
make install-launchd    # install + register the daily launchd agent (pull then push; runs now, 03:00, login; bakes in CODEX_HOME if set)
make uninstall-launchd  # unregister the launchd agent
make test               # go test -v ./...
make check              # pre-commit equivalent: gofmt -l, go vet, go test -short
make fmt                # go fmt ./...
make lint                # golangci-lint run (requires golangci-lint installed)
make build-all           # cross-compile darwin/linux/windows × arm64/amd64 into ./bin/
make setup-hooks         # install .githooks/pre-commit (runs on every commit)

# Run a single test
go test -v -run TestName ./internal/sync/
go test -v ./internal/crypto/ -run TestGenerateKeyFromPassphrase

# Integration tests (require real R2 credentials; behind build tag)
go test -tags=integration -v ./integration/...
# or: cd integration && docker-compose up --build
```

Version is injected at build time: `-ldflags "-X main.version=<version>"`. `make build` pulls the version from `git describe --tags --always --dirty`.

## Architecture

Layered, with a pluggable storage abstraction:

- **CLI layer** — `cmd/codex-sync/main.go`. Cobra commands (`init`, `push`, `pull`, `status`, `diff`, `conflicts`, `paths`, `reset`, `update`, `changelog`) plus Survey-driven interactive wizards. `paths` (`add`/`remove`/`exclude`/`unexclude`/`list`/`reset`) is backed by `internal/paths.Manager`, which edits the `sync_paths`/`exclude` fields in `config.yaml`. All user-facing output lives here.
- **Sync layer** — `internal/sync/`. `Syncer` orchestrates push/pull; `SyncState` (`state.json`) tracks per-file SHA256 hash + size + mtime + last-uploaded time. `DetectChanges` compares local files against state to produce `add/modify/delete` work items. Push/pull both run uploads/downloads with a worker pool (`defaultWorkers = 10`). Two files merge instead of conflicting: `session_index.jsonl` and `history.jsonl` are routed by `IsMergeablePath` to `mergeRemote`/`MergeJSONL` (`merge.go`), which unions entries by `id` (session index — latest `updated_at` wins) or by distinct line ordered by `ts` (history); every tie is broken on the raw line bytes so both devices converge byte-for-byte. Pull also removes local files that vanished from the remote: `staleLocalFiles` finds files tracked in state but absent from the remote listing, and `moveToTrash` relocates the ones unchanged since the last sync to `~/.codex-sync/trash/<batch>/`; changed ones are left in place and reported instead. An empty remote object listing short-circuits `Pull()` before any of this runs, so it can never wipe local files.
- **Crypto layer** — `internal/crypto/encrypt.go`. Wraps `filippo.io/age` (X25519 + ChaCha20-Poly1305). Supports two key modes: random (`GenerateKey`) or passphrase-derived (`GenerateKeyFromPassphrase`, Argon2id with a **fixed salt** `sha256("codex-sync-v1")` — deliberately different from claude-sync's `claude-sync-v1` — so the same passphrase yields the same key on any device, and a different key than claude-sync). The derived 32 bytes are clamped for X25519 then Bech32-encoded as an `AGE-SECRET-KEY-…` identity.
- **Storage layer** — `internal/storage/`. `Storage` interface (`Upload`/`Download`/`Delete`/`DeleteBatch`/`List`/`Head`/`BucketExists`) with four adapters: `r2/`, `s3/` (also serves S3-compatible endpoints via a custom `Endpoint`), `gcs/`, `webdav/`. Adapters **self-register** via `init()` functions setting package-level `storage.NewR2` / `NewS3` / `NewGCS` / `NewWebDAV` vars; `cmd/codex-sync/main.go` blank-imports them to wire up the factory (`storage.New`). Add new providers by following this pattern.
- **Config layer** — `internal/config/config.go`. YAML at `~/.codex-sync/config.yaml` (perms 0600). `BaseDirE()` resolves the Codex home: `$CODEX_HOME` when set, else `~/.codex`. `SyncPaths`/`SessionSyncPaths` define the Codex profile (the `full` and `sessions` scopes); `HardExcludes` always apply even when a parent directory is listed in `sync_paths` (every `*.sqlite*`, `auth.json`, `installation_id`, caches, logs, `plugins/`, `packages/`, and more); `ProtectedPaths` (`auth.json`, `installation_id`) are additionally never uploaded and never written by pull. `GetEffectiveSyncPaths()` treats scope as a ceiling: under `sessions` scope, a custom `sync_paths` list is intersected with `SessionSyncPaths`, never widened. Supports both the new unified `storage:` block and legacy R2-only top-level fields — `GetStorageConfig()` upgrades the legacy form automatically.

### On-disk layout

```
~/.codex-sync/   # tool's own state (perms 0600/0700)
├── config.yaml  # storage + encryption config
├── age-key.txt  # encryption identity (derived or random)
├── state.json   # per-file hash/size/mtime + last push/pull times
└── trash/       # files pull removed locally, one batch dir per run (never touched by `reset`)

~/.codex/        # what gets synced (see config.SyncPaths)
```

### Sync semantics

- **Remote keys** are local paths with `.age` appended (home-relative-tokenized; a no-op in practice, since rollout filenames carry dates and UUIDs rather than paths).
- **Push** encrypts only files whose current hash differs from state; deletions detected from state are batched via `DeleteBatch`.
- **Pull** downloads when the local file is missing, or when remote `LastModified` is after the state's `Uploaded` time. If the local hash **also** differs from state (both sides changed), it's a **conflict**: local is kept, remote is written to `<path>.conflict.<timestamp>`. `codex-sync conflicts` resolves them (and updates state on resolution). Sidecars are local artifacts: `*.conflict.*` is a hard exclude and `handleConflict` drops the sidecar's state entry, so they are never uploaded, tracked, or trashed by a later pull — and **push skips a file that still has a live sidecar**, reporting `unresolved conflict for <path>` instead of overwriting the remote.
- **Errors fail the command**: push/pull print per-file errors to stderr even with `-q` and exit non-zero when any file failed (`reportSyncErrors` in `main.go`), so the launchd chain `pull -q && push -q` stops and the log says why.
- **Merge on pull**: `session_index.jsonl` and `history.jsonl` are unioned instead of conflicted whenever the remote copy changed since the last sync (or was never seen locally); the merged result is written back locally and recorded in state so the next push uploads the union.
- **Removal on pull**: a file tracked in state but absent from the remote listing is moved to `~/.codex-sync/trash/<batch>/` when its on-disk hash still matches the last-synced hash; a locally modified file is left in place and reported instead. `pull --no-delete` disables this. Pull returns immediately on an empty remote listing, before removal logic runs, so a temporarily empty bucket can never delete local files.
- **First pull with existing local files** is handled specially in `cmd/codex-sync/main.go` (`handleFirstPullWithExistingFiles`): shows a preview diff and offers backup-to-`~/.codex.backup.<ts>`/overwrite/abort.
- **Backward-compat read path**: decrypt always attempts gzip decompression only if magic `0x1f 0x8b` is present, so older uncompressed remote blobs still work. Write path always compresses.
- **Key verification**: during `init`, after deriving the key, `verifyKeyMatchesRemote` downloads a small remote file and tries to decrypt it. Mismatch triggers the 3-way prompt (retry passphrase / clear remote / abort).

## Distribution

No npm package and no pre-built binaries are published yet — build from source (`make build`, `make install`). `codex-sync update` and `codex-sync changelog` read releases from this repository's own GitHub Releases API (`d-jiao/codex-sync`); until a release exists, both print a build-from-source hint instead of failing. `update` additionally verifies the downloaded binary against the release's `checksums.txt` when one is published.

## CI & pre-commit

- `.github/workflows/ci.yml` runs `go test -v ./...`, enforces **60% coverage** on the `internal/*` packages, builds, and runs golangci-lint. Keep changes to `internal/` covered.
- `.githooks/pre-commit` (enabled by `make setup-hooks`) runs `gofmt -l`, `go vet`, `go test ./... -short`, and golangci-lint if installed. Run `make check` locally before committing to mirror it.

## Gotchas

- **Rollout filenames are already portable.** They're named by date and UUID (`sessions/YYYY/MM/DD/rollout-<ts>-<uuid>.jsonl`), not by project path, so remote-key normalization is a no-op for them — unlike claude-sync, which encoded the absolute project path into its own session directory names.
- **File content is still `${HOME}`-tokenized.** The `cwd` fields inside rollout/history/session-index JSONL, and paths in `config.toml`/`AGENTS.md`, are rewritten to a portable token on upload and resolved back to the real home on pull (`IsPortableContentPath`, `internal/sync/paths.go`). Identical home directories make this a byte-identical no-op; different ones are rewritten automatically.
- **Keep the Codex engine version aligned across machines.** An older engine silently drops threads written by a newer one (design spec §2); codex-sync does not detect or warn about version skew.
- **Never sync a `*.sqlite*` file.** Codex rebuilds every database from the rollout files on startup; syncing one would ship stale, machine-local state. `HardExcludes` blocks these even if a parent directory is added to `sync_paths`.
- **The Argon2 salt is `sha256("codex-sync-v1")` and must never change.** It's fixed intentionally, so a passphrase derives the same key on every device, and deliberately different from claude-sync's `claude-sync-v1` for domain separation — the same passphrase must not unlock both tools' data. Changing it breaks sync for every existing user.
- **The trash-directory guard**: `moveToTrash` refuses to run when no trash directory is configured, rather than deleting the file outright. `Syncer.trashDir` defaults to `config.TrashDirPath()`; tests that exercise pull removal must set it explicitly (`SetTrashDir`).
- **Storage adapter imports**: anything outside `cmd/codex-sync/` that calls `storage.New(...)` must also blank-import the adapter packages it needs (see `internal/sync/sync.go` top). Forgetting this produces a runtime "unsupported storage provider" error, not a compile error.
- **Symlinks are skipped** by `GetLocalFiles` — don't rely on symlinked content inside `~/.codex/` being synced.
- **Do not add destructive operations** to the default code path without an explicit `--force` or interactive confirm — the CLI is careful about backups (`~/.codex.backup.<ts>`), key-mismatch detection, and `.conflict.<ts>` files for a reason.

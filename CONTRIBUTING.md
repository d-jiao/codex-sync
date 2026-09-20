# Contributing to codex-sync

Thanks for your interest. This page covers how to get a working build, what a
good pull request looks like, and the few things in this codebase that must not
change. For an architecture tour, read [CLAUDE.md](CLAUDE.md); for the design
rationale, read
[docs/specs/2026-09-15-codex-sync-design.md](docs/specs/2026-09-15-codex-sync-design.md).

## Before you start

- **Questions and bugs** → open an issue. Include your Codex version, macOS
  version, storage provider, and the output of the failing command (run it
  without `-q`; credentials are never printed).
- **Larger changes** → open an issue first to agree on the approach. Things
  that touch the sync semantics (merge rules, removal, conflicts) or the crypto
  need a short design note before code.
- **Security problems** → see [SECURITY.md](SECURITY.md); please don't open a
  public issue.

## Development setup

Requirements: Go 1.24+ (pinned in `go.mod`), `make`, and optionally
[golangci-lint](https://golangci-lint.run/) (CI runs it; `make check` skips it
when not installed).

```bash
git clone https://github.com/d-jiao/codex-sync
cd codex-sync
make setup-hooks        # pre-commit: gofmt, go vet, go test -short, golangci-lint if present
make build              # ./bin/codex-sync
make test               # go test -v ./...
make check              # what the pre-commit hook and CI run
```

Run a single test with `go test -v -run TestName ./internal/sync/`.

A development build reads the same `~/.codex-sync/config.yaml` and `~/.codex`
as your installed binary. To try things without touching either, run it with
`HOME` pointed at a scratch directory and a scratch bucket:

```bash
mkdir -p /tmp/cs-sandbox && HOME=/tmp/cs-sandbox ./bin/codex-sync init
```

(`CODEX_HOME=...` overrides only the Codex home, not the tool's own state.)

## Repository layout

| Path | What lives there |
|---|---|
| `cmd/codex-sync/` | Cobra commands, wizards, all user-facing output (`main.go`, `desktop.go`) |
| `internal/sync/` | push/pull engine, per-file state, JSONL merges, trash, `${HOME}` path portability |
| `internal/crypto/` | age encryption, passphrase key derivation |
| `internal/storage/` | `Storage` interface and the `r2/`, `s3/`, `gcs/`, `webdav/` adapters |
| `internal/config/` | `~/.codex-sync/config.yaml`, the Codex sync profile, hard excludes |
| `internal/desktop/` | `desktop refresh`: re-index, thread names, catalog reset for the ChatGPT app |
| `internal/paths/` | `codex-sync paths` (sync_paths / exclude editing) |
| `integration/` | real-storage tests (build tag `integration`) and the manual acceptance script |
| `scripts/launchd/` | the daily launchd agent template |
| `scripts/release-notes.sh` | extracts one version's CHANGELOG section for the release workflow |
| `docs/` | design specs and the security model |

## Making a change

1. Branch from `main`.
2. Write the test first where you can. Every package under `internal/` counts
   toward CI's **60% coverage floor**; new behavior there needs tests. The
   in-memory `mockStorage` in `internal/sync/sync_push_pull_test.go` is the way
   to test push/pull without a bucket.
3. Keep `make check` green. CI additionally runs golangci-lint with its default
   linters, so run it locally if you have it.
4. Update the docs that describe what you changed: `README.md` for user-visible
   behavior, `CLAUDE.md` for architecture or gotchas, and the **Unreleased**
   section of `CHANGELOG.md`.
5. Commit messages follow the existing style: `type(scope): summary` in the
   imperative, e.g. `fix(sync): stamp uploads with the remote's timestamp`.
   Types in use: `feat`, `fix`, `docs`, `test`, `refactor`, `ci`, `chore`.
6. Open a pull request against `main` with what changed and why, and how you
   tested it (unit tests, or a real two-machine run).

## Things that must not change

These are load-bearing; changing them breaks existing users or their data.

- **The Argon2 salt** in `internal/crypto/encrypt.go` is
  `sha256("codex-sync-v1")`. Changing it silently derives a different key on
  every existing install, and it is deliberately different from claude-sync's
  so one passphrase cannot unlock both tools' data.
- **Never sync a `*.sqlite*` or `*.db*` file.** Codex rebuilds every database
  from the rollout files; syncing one ships stale, machine-local state.
  `config.HardExcludes` enforces this even when a user adds a parent directory.
- **`auth.json` and `installation_id` are protected**: never uploaded, never
  written by pull.
- **No destructive operation on a default code path** without `--force` or an
  interactive confirmation. Pull moves files to `~/.codex-sync/trash/` rather
  than deleting them, returns early on an empty remote listing, and offers a
  backup before its first overwrite. Keep it that way.
- **Merges must be deterministic.** `MergeJSONL` breaks every tie on the raw
  line bytes so two machines converge byte-for-byte; never on arrival order.
- **Conflict sidecars stay local.** `*.conflict.*` is a hard exclude; push
  skips a file with a live sidecar instead of overwriting the remote.

## Testing against real storage

Unit tests need no credentials. Two extra checks do:

**Integration tests** (build-tag gated) run a two-device sync against a real
R2 bucket — use a scratch bucket, never your sync bucket:

```bash
export CODEX_SYNC_R2_ACCOUNT_ID=...
export CODEX_SYNC_R2_ACCESS_KEY_ID=...
export CODEX_SYNC_R2_SECRET_ACCESS_KEY=...
export CODEX_SYNC_R2_BUCKET=codex-sync-test
go test -tags=integration -v ./integration/...
```

See [integration/README.md](integration/README.md) for the Docker variant.

**Acceptance script.** `integration/codex_listing_check.py` asks a real Codex
engine (over the `app-server` protocol) to list the user-visible threads in two
`$CODEX_HOME` directories — a source home and a synced copy — and compares
them. Run it against *copies* (or APFS clones) of the homes, never the live
`~/.codex`: the engine writes state into whichever home it is given.

```bash
integration/codex_listing_check.py --source /path/to/copy-of-home \
    --synced /path/to/copy-of-other-home \
    [--codex-bin /Applications/ChatGPT.app/Contents/Resources/codex]
```

It exits 2 with `codex engine binary not found` when no engine can be started
(set `--codex-bin` or `CODEX_BIN`).

## Cutting a release

Releases are driven by the tag. Move the accumulated **Unreleased** entries in
`CHANGELOG.md` under a `## [X.Y.Z] - YYYY-MM-DD` heading, leave a fresh empty
**Unreleased** above it, commit, then:

```bash
git tag -a vX.Y.Z -m "codex-sync vX.Y.Z"
git push origin main --follow-tags
```

`.github/workflows/release.yml` takes it from there: it extracts the changelog
section for the tag, runs `make check`, cross-compiles the six platform
binaries with `VERSION` set to the tag, verifies the built binary reports that
version and that `checksums.txt` matches, then publishes the GitHub release with
every binary and `checksums.txt` attached.

Three things the workflow deliberately refuses to do. It fails before building
if the tag has no changelog section with content, so an undocumented release
cannot ship. It fails if the binaries do not report the tag version, which
catches broken `VERSION` plumbing. And a tag containing a hyphen
(`v0.2.0-rc.1`) is published as a prerelease, because `codex-sync update`
follows the `releases/latest` endpoint and GitHub excludes prereleases from it.

The release assets are the update mechanism: `codex-sync update` looks for an
asset named exactly `codex-sync-<goos>-<goarch>` (plus `.exe` on Windows) and
verifies it against the `checksums.txt` asset, aborting on a mismatch and only
warning when checksums are absent. Renaming an asset breaks self-update.

To rehearse without publishing, run `scripts/release-notes.sh vX.Y.Z` to see the
notes and `make build-all VERSION=vX.Y.Z` to produce `bin/` locally.

## Relationship to claude-sync

codex-sync was seeded from [tawanorg/claude-sync](https://github.com/tawanorg/claude-sync)
at commit `49420ef` with full history (see [NOTICE](NOTICE)). The storage,
crypto and state engine are shared; the sync profile, merge rules, removal
logic, desktop refresh and CLI are Codex-specific. Engine fixes from upstream
come over by cherry-pick, never by merging `upstream/main` (which carries
Claude-specific work):

```bash
git fetch upstream
git log --oneline --no-merges main..upstream/main -- internal cmd
git cherry-pick -x <sha>
# on import-path conflicts:
# sed -i '' 's#github.com/tawanorg/claude-sync#github.com/d-jiao/codex-sync#g' <files>
```

## License

By contributing you agree that your contributions are licensed under the MIT
License, like the rest of the project (see [LICENSE](LICENSE)).

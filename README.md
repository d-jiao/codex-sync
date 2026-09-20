# codex-sync

[![CI](https://github.com/d-jiao/codex-sync/actions/workflows/ci.yml/badge.svg)](https://github.com/d-jiao/codex-sync/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

Encrypted cross-device sync for [OpenAI Codex](https://github.com/openai/codex)
local state. Start a conversation on one Mac, continue it on another; keep your
config, rules, skills and memories in step.

- **Your storage, your key.** Files are gzip-compressed and
  [age](https://github.com/FiloSottile/age)-encrypted *before* they leave the
  machine. The bucket (Cloudflare R2, S3, GCS, any S3-compatible service, or
  WebDAV) only ever sees ciphertext.
- **Merge, don't clobber.** Thread names and prompt history are unioned across
  machines; a file changed on both sides becomes a conflict you resolve, never a
  silent overwrite.
- **Careful by default.** First pull offers a backup, removals go to a trash
  directory, an empty bucket can never wipe your local files.

codex-sync is a fork of [claude-sync](https://github.com/tawanorg/claude-sync)
(MIT) with the sync engine kept and everything Codex-specific rebuilt — see
[NOTICE](NOTICE). It is early software (v0.x): macOS, built from source, tested
by its author on two Macs. Issues and pull requests are welcome.

## Contents

- [How it works](#how-it-works)
- [Install](#install)
- [Set up](#set-up)
- [Everyday use](#everyday-use)
- [What gets synced](#what-gets-synced)
- [How sync behaves](#how-sync-behaves)
- [Seeing pulled threads in the desktop app](#seeing-pulled-threads-in-the-desktop-app)
- [Troubleshooting](#troubleshooting)
- [Security](#security)
- [Limitations](#limitations)
- [Contributing](#contributing)
- [License](#license)

## How it works

Codex keeps everything that matters in plain files under `~/.codex` (or
`$CODEX_HOME`): one JSONL "rollout" file per conversation, an index of thread
names, your prompt history, `config.toml`, rules, skills and memories. Its
SQLite databases are derived from those files and are rebuilt on startup.

codex-sync treats the files as the source of truth:

1. **`push`** encrypts every file that changed since the last sync and uploads
   it to your bucket; files you deleted locally are reported and kept in
   storage until you run `push --force`.
2. **`pull`** downloads what changed remotely, merges the two shared index
   files, saves both-sides-changed files as conflict sidecars, and moves files
   that vanished from the remote to a trash directory.
3. A small state file (`~/.codex-sync/state.json`) remembers each file's hash so
   both commands only touch what changed.

Two things to know up front:

- **Keep every machine on the same Codex version.** An older engine silently
  hides threads written by a newer one.
- **The ChatGPT desktop app does not notice pulled threads on its own.** Run
  `codex-sync pull --desktop` (with the app quit) — see
  [below](#seeing-pulled-threads-in-the-desktop-app).

## Install

Requirements: macOS, [Go](https://go.dev/dl/) 1.24 or newer, and a bucket at
one of the supported providers (next section). There are no pre-built binaries
or packages yet.

```bash
git clone https://github.com/d-jiao/codex-sync
cd codex-sync
make build && make install    # installs ~/.local/bin/codex-sync (override with INSTALL_DIR=...)
codex-sync --version
```

Make sure `~/.local/bin` is on your `PATH`.

## Set up

### 1. Pick a storage provider

| Provider | Free tier | Best for |
|---|---|---|
| **Cloudflare R2** (recommended) | 10 GB storage, 1M writes, 10M reads/month, no egress fees | personal use |
| **AWS S3** | 5 GB for 12 months, then ~$0.023/GB | AWS users |
| **Google Cloud Storage** | 5 GB, 5K writes, 50K reads/month | GCP users |
| **S3-compatible** | varies | Backblaze B2, MinIO, Wasabi, DigitalOcean Spaces, self-hosted |
| **WebDAV** | your own server | Nextcloud / ownCloud users |

Storage needs scale with your conversation history: `sessions/` and
`archived_sessions/` range from a few MB to a few hundred MB on a long-lived
install, which fits in every free tier above.

### 2. Create a bucket and credentials

<details>
<summary><b>Cloudflare R2</b></summary>

1. [Cloudflare Dashboard](https://dash.cloudflare.com/) → R2 Object Storage → **Create bucket** (e.g. `codex-sync`)
2. **Manage R2 API Tokens** → **Create API Token** with **Object Read & Write** permission

You'll need: Account ID, Access Key ID, Secret Access Key.
</details>

<details>
<summary><b>AWS S3</b></summary>

1. [S3 Console](https://s3.console.aws.amazon.com/s3/bucket/create) → Create bucket
2. [IAM Security Credentials](https://console.aws.amazon.com/iam/home#/security_credentials) → Create access keys

You'll need: Access Key ID, Secret Access Key, Region.
</details>

<details>
<summary><b>Google Cloud Storage</b></summary>

1. [Cloud Storage](https://console.cloud.google.com/storage/create-bucket) → Create bucket
2. [Service Accounts](https://console.cloud.google.com/iam-admin/serviceaccounts) → Create service account with the **Storage Object Admin** role → Create JSON key

You'll need: Project ID and the service-account JSON file (or run `gcloud auth application-default login`).
</details>

<details>
<summary><b>S3-compatible</b> (Backblaze B2, MinIO, Wasabi, DigitalOcean Spaces, …)</summary>

Any provider with an S3-compatible API works through the **S3-compatible
(custom endpoint)** option. Create a bucket and an application key with your
provider, then pass its S3 endpoint URL:

```bash
codex-sync init --provider s3-compatible --endpoint https://s3.us-west-004.backblazeb2.com
```

You'll need: Endpoint URL, Access Key ID, Secret Access Key, Bucket. The signing
region is auto-detected from the endpoint (e.g. `us-west-004`); providers that
ignore it get `auto`.

For servers that don't resolve buckets as subdomains (Ceph RGW, MinIO without
wildcard DNS), add `--use-path-style` to address objects as
`endpoint/bucket/key` instead of `bucket.endpoint/key`:

```bash
codex-sync init --provider s3-compatible --endpoint https://ceph.example.com --use-path-style
```

It is off by default and not needed for Backblaze B2, Wasabi or DigitalOcean
Spaces. Custom endpoints also relax the AWS SDK's default integrity-checksum
headers, which some providers reject; AWS S3 itself is unaffected.
</details>

<details>
<summary><b>WebDAV</b> (Nextcloud, ownCloud, …)</summary>

No bucket to create — point at your existing server.

1. Nextcloud: Settings → Security → Devices & sessions → **Create app password**
2. Note your WebDAV URL: `https://your-server/remote.php/dav/files/USERNAME/`

You'll need: WebDAV URL, Username, App password. The wizard creates a
`codex-sync` subdirectory for you.

Object names are percent-encoded per path segment, so attachments whose names
contain spaces, `#`, `?` or `%` upload, list and download correctly.
</details>

### 3. Initialize on the first machine

```bash
codex-sync init
```

The wizard walks you through:

1. **Storage provider** and its credentials (all of them can also be passed as
   flags — see `codex-sync init --help`).
2. **Encryption key.** *Passphrase* (recommended, at least 12 characters):
   the same passphrase produces the same key on every machine, so there is
   nothing to copy around. *Random key*: no passphrase to remember, but you
   must copy `~/.codex-sync/age-key.txt` to each machine yourself.
3. **Connection test** against the bucket.
4. **Sync scope** — `full` (everything in the table under
   [What gets synced](#what-gets-synced)) or `sessions` (conversations only).
   Skip the question with `--scope full` or `--scope sessions`.

Then upload:

```bash
codex-sync push
```

### 4. Initialize on the other machine

Run `codex-sync init` with the **same passphrase**, then pull:

```bash
codex-sync init
codex-sync pull --dry-run   # optional: preview
codex-sync pull
```

`init` verifies the passphrase by decrypting a file from the bucket before it
finishes, so a typo is caught here rather than on pull. If this machine already
has files in `~/.codex`, pull shows what would change and offers to back them
up to `~/.codex.backup.<timestamp>` first — see
[First pull onto a machine that already has files](#first-pull-onto-a-machine-that-already-has-files).

### 5. Optional: sync daily with launchd

```bash
make install-launchd     # installs the binary, then the agent
make uninstall-launchd   # removes the agent
```

This registers a per-user launchd agent
(`~/Library/LaunchAgents/com.codex-sync.daily.plist`) that runs
`codex-sync pull -q && codex-sync push -q` right away, then daily at 03:00 and
at every login. Output goes to `~/Library/Logs/codex-sync.log`; when a file
fails, the job exits non-zero and the log says which file and why.

The scheduled job never passes `push --force`: an unattended run must not be
the thing that deletes remote files. It pulls first anyway, which restores
anything you deleted locally, so deleting a file on every machine is a
deliberate `codex-sync push --force`.

launchd jobs do not see your shell environment. If you use a custom Codex home,
run `CODEX_HOME=/path/to/home make install-launchd` — the value is baked into
the agent at install time (re-run the target to change it). There is no
scheduler built into the CLI itself.

## Everyday use

```bash
codex-sync push               # upload local changes
codex-sync pull               # download remote changes
codex-sync pull --desktop     # …and make new threads visible in the ChatGPT app (quit it first)
codex-sync status             # what push would upload
codex-sync diff               # local vs remote
codex-sync conflicts          # list and resolve conflicts
```

| Command | What it does |
|---|---|
| `init` | Set up storage, key and scope (interactive wizard) |
| `push` | Upload local changes; report files removed locally (`--force` deletes them remotely) |
| `pull` | Download remote changes; merge, conflict or trash as described below |
| `desktop refresh` | Make pulled threads visible in the ChatGPT desktop app |
| `status` | Show pending local changes |
| `diff` | Show differences between local and remote |
| `conflicts` | List and resolve `.conflict.*` sidecars |
| `paths` | Manage sync paths and exclude filters |
| `reset` | Remove local config, key and sync state (keeps `trash/`) |
| `update` | Update to the latest release (verifies `checksums.txt`) |
| `changelog` | Show release history |

Every command takes `-q` / `--quiet` for scripts. Errors are still printed to
stderr, and a push or pull with any failed file exits non-zero, so
`pull -q && push -q` stops at the first problem.

### Useful flags

```bash
codex-sync pull --dry-run         # preview downloads, merges, conflicts and removals
codex-sync pull --force           # skip the first-pull confirmation prompt
codex-sync pull --no-delete       # never move local files to the trash
codex-sync pull --desktop         # refresh the desktop app after a successful pull

codex-sync push --force           # also delete the remote copies of files deleted locally

codex-sync init --passphrase      # re-enter the passphrase only (keeps storage config)
codex-sync init --force           # start over: overwrite config and key
codex-sync init --scope sessions  # conversations only

codex-sync conflicts --list       # just list
codex-sync conflicts --keep local | remote   # resolve all one way

codex-sync reset                  # remove config.yaml, age-key.txt, state.json
codex-sync reset --remote         # …and delete every object in the bucket
```

`reset` never touches `~/.codex` and never touches `~/.codex-sync/trash/`, so
files an earlier pull removed stay recoverable. Run `codex-sync init` afterwards
to set up again.

`update` and `changelog` read this repository's GitHub Releases. Until the first
release exists they print a build-from-source hint
(`git pull && make build && make install`) instead of failing.

### Choosing what to sync

```bash
codex-sync paths                    # list sync paths and exclude filters
codex-sync paths add <path>         # add a path under ~/.codex
codex-sync paths remove <path>      # stop syncing a path
codex-sync paths exclude <glob>     # skip a glob pattern inside a synced directory
codex-sync paths unexclude <glob>   # remove a glob filter
codex-sync paths reset              # back to the defaults
```

Excludes are globs matched against paths relative to `~/.codex` and can also be
edited directly in `~/.codex-sync/config.yaml`:

```yaml
exclude:
  - "*.tmp"
  - "attachments/**"
  - "skills/**/node_modules/**"
```

The scope chosen at `init` is a ceiling: under `sessions` scope, `paths add`
rejects a path outside the conversation set rather than silently widening it.

## What gets synced

| Path (under `~/.codex`) | Scope | Notes |
|---|---|---|
| `sessions/`, `archived_sessions/` | sessions | conversations (rollout files; the source of truth) |
| `session_index.jsonl` | sessions | thread names — merged, never overwritten |
| `history.jsonl` | sessions | prompt history — merged, never overwritten |
| `attachments/` | sessions | files you attached to threads |
| `config.toml` | full | providers, MCP servers, project trust |
| `rules/`, `skills/`, `memories/`, `AGENTS.md` | full | |

| Scope | Syncs | Use when |
|---|---|---|
| `full` (default) | everything in the table above | you want config, rules, skills and memories mirrored too |
| `sessions` | the rows marked *sessions* only | you just want conversations to continue across machines |

**Never synced**, even if you add a parent directory to the sync paths:
`auth.json` and `installation_id` (your login and device identity), every
`*.sqlite*` / `*.db*` file (Codex rebuilds them from the rollout files),
`plugins/`, `packages/`, `cache/`, logs, worktrees and other runtime state, and
`*.conflict.*` sidecars.

Home directories are portable: `cwd` fields inside rollout, history and index
files, and paths in `config.toml` / `AGENTS.md`, are rewritten to a `${HOME}`
token on upload and resolved back on pull, so two machines with different
usernames still work.

## How sync behaves

### Push

Uploads files whose content changed since the last sync. A file that still has
a live `.conflict.*` sidecar is skipped and reported as an error until you
resolve it with `codex-sync conflicts`; the sidecar itself is never uploaded.

**Files deleted locally are not deleted remotely by default.** A stale
checkout, a restored backup or a half-configured `sync_paths` would otherwise
erase the copy every other machine pulls from. Push lists them instead:

```
2 file(s) deleted locally are still in storage:
  • sessions/rollout-2026-09-12.jsonl
  • memories/old-note.md
  Run codex-sync push --force to delete them remotely too.
```

`push --force` deletes them, but still refuses to remove an object that
another machine has replaced since your last sync — that file is reported as a
conflict and left in storage. Pull it first, then push again. Where the
provider supports it (S3, R2, GCS, and WebDAV servers that honour `If-Match`),
the delete is also conditional on the object version, so a device that uploads
in the middle of your push keeps its copy.

### Pull

- **New and changed remote files are downloaded.**
- **Both sides changed → conflict.** The local file is kept and the remote copy
  is saved next to it as `<file>.conflict.<timestamp>`. Sidecars are local
  only: never uploaded, tracked, or removed by a later pull.
- **`session_index.jsonl` and `history.jsonl` are merged**, not conflicted: the
  remote and local copies are unioned (by thread id, latest `updated_at` wins;
  by distinct line ordered by `ts`), written back locally, and pushed as the
  union next time. Both machines converge byte-for-byte.
- **Files that vanished from the remote** (deleted or archived on the other
  machine) are moved to `~/.codex-sync/trash/<timestamp>/` — but only when
  unchanged locally since the last sync. Locally modified files stay put and
  are reported. `--no-delete` disables this, `--dry-run` previews it. Nothing
  references the trash, so old batches are safe to delete.
- **An empty remote never removes anything.**
- **Pull writes are atomic and stay inside `~/.codex`.** Each file is written
  to a temporary file and renamed into place, so an interrupted pull leaves
  either the old file or the new one. A path that crosses a symlink is
  refused and reported rather than followed, which matches push: it skips
  symlinks instead of uploading what they point at.

### Conflicts

```bash
codex-sync conflicts                # interactive
codex-sync conflicts --list         # just list
codex-sync conflicts --keep local   # keep every local version
codex-sync conflicts --keep remote  # keep every remote version
```

Interactive keys: **l** keep local, **r** keep remote, **d** show diff,
**s** skip, **q** quit. Resolving a conflict removes the sidecar; the next push
publishes whichever version you kept.

### First pull onto a machine that already has files

When `~/.codex` already has content, pull:

1. shows what would be overwritten, kept, merged or downloaded;
2. asks whether to **back up**, **overwrite** or **abort**;
3. on *back up*, copies the existing files to `~/.codex.backup.<timestamp>`
   first. The backup holds the same set of files a push would upload, so
   `auth.json`, the SQLite databases and anything you excluded stay out of it.

`pull --dry-run` shows the preview without changing anything; `pull --force`
skips the prompt (for scripts).

## Seeing pulled threads in the desktop app

**Why this step exists.** The ChatGPT desktop app keeps its own thread catalog.
After a one-time full build it only looks at threads newer than the last one it
saw, so a thread that arrives via sync — whose timestamps are older — never
appears in the sidebar on its own. Codex itself never re-reads the synced
index either, so pulled threads also show up unnamed in the CLI.

**What to do.** Quit the ChatGPT app, then either:

```bash
codex-sync pull --desktop      # pull, then refresh
codex-sync desktop refresh     # refresh only, after an earlier pull
```

Relaunch the app and the pulled threads appear, with their names.

**What the refresh does**, in order:

1. Backs up the Codex databases and `session_index.jsonl` to
   `~/.codex-sync/db-backup-<timestamp>/` (`--no-backup` skips this).
2. Runs the app's own engine once so it indexes every rollout on disk.
3. Copies thread names from the synced `session_index.jsonl` into the engine
   database for threads that have none (`--no-names` skips this).
4. Schedules the app's full catalog sweep for its next launch.

**Good to know:**

- It refuses to run while the ChatGPT app or any `codex` process is open, since
  they hold the databases. Detection compares each process's executable name,
  so an app bundle installed under a path containing spaces is found too.
  `pull --desktop` still completes the pull in that case, then reports the
  running app and exits non-zero — so `pull --desktop && push` stops there;
  quit the app and run `codex-sync desktop refresh`.
- It is safe to run repeatedly.
- The engine used is the ChatGPT app's bundled one; `--codex-bin` or
  `$CODEX_BIN` override it. A different engine version may migrate every Codex
  database, which is why all of them are backed up (except the engine's log
  store).
- Backups accumulate — each run writes a full copy, well over 100 MB with a
  large history — and nothing references them, so old `db-backup-*`
  directories are safe to delete.

## Troubleshooting

**Wrong passphrase on a new machine.** `init` verifies the passphrase against
the bucket and offers to retry; afterwards, re-enter it without redoing the
storage setup:

```bash
codex-sync init --passphrase
```

**Forgot the passphrase.** It is never stored anywhere, and the encrypted
files cannot be recovered without it. Start over:

```bash
codex-sync reset --remote   # delete remote files and local config/key/state (trash/ kept)
codex-sync init             # set up again with a new passphrase
codex-sync push             # re-upload from this machine
```

**A thread I continued on both Macs came back as a conflict.** Expected: two
machines appended to the same rollout file between syncs. Pick a side with
`codex-sync conflicts`; the other transcript is in the sidecar. Avoid resuming
the same thread on two machines between syncs.

**Pulled threads are missing from the desktop app.** See
[Seeing pulled threads in the desktop app](#seeing-pulled-threads-in-the-desktop-app).
If `desktop refresh` says the app is running, quit it (and any `codex` process)
and run it again.

**The launchd job stopped syncing.** Check `~/Library/Logs/codex-sync.log`;
the last lines name the file that failed and why. An unresolved conflict makes
push fail until you run `codex-sync conflicts`.

**Getting back a file that pull removed.** Look in
`~/.codex-sync/trash/<timestamp>/`; files keep their relative path.

## Security

Same model as claude-sync: gzip → age (X25519 / ChaCha20-Poly1305), encrypted
locally before upload, so the storage provider only sees ciphertext and
filenames. Passphrase keys are derived with Argon2id and the fixed salt
`sha256("codex-sync-v1")`, so the same passphrase gives the same key on every
machine — and a different key than claude-sync would derive. Config, key and
state live in `~/.codex-sync/` with `0600`/`0700` permissions; note that the
storage credentials in `config.yaml` are stored in plaintext there.

Details, threat model and the inherited audit: [docs/security.md](docs/security.md)
and [SECURITY-AUDIT.md](SECURITY-AUDIT.md). To report a vulnerability, see
[SECURITY.md](SECURITY.md).

## Limitations

- Pulled threads do not appear in the ChatGPT desktop app, and carry no name in
  the CLI, until `codex-sync desktop refresh` (or `pull --desktop`) runs with
  the app quit.
- Do not resume the same thread on two machines between syncs; you get a
  conflict sidecar instead of a merged transcript.
- Project organization, automations and the memories database live only in
  SQLite and are not synced.
- macOS only for now: the desktop refresh and the launchd scheduler are
  macOS-specific, and the sync commands are untested elsewhere.
- No pre-built binaries or packages yet — build from source.

## Contributing

Bug reports, questions and pull requests are welcome. See
[CONTRIBUTING.md](CONTRIBUTING.md) for the build/test workflow and
[CLAUDE.md](CLAUDE.md) for an architecture tour; the design rationale is in
[docs/specs/2026-09-15-codex-sync-design.md](docs/specs/2026-09-15-codex-sync-design.md).

```bash
make test          # run the tests
make check         # gofmt, go vet, go test -short (what the pre-commit hook runs)
make setup-hooks   # install the pre-commit hook
```

## License

MIT — see [LICENSE](LICENSE). codex-sync is a fork of
[claude-sync](https://github.com/tawanorg/claude-sync); see [NOTICE](NOTICE)
for the fork point and attribution.

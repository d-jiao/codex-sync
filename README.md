# codex-sync

Encrypted cross-device sync for OpenAI Codex local state. Continue a Codex
conversation on another Mac; keep config, rules, skills and memories in step.

codex-sync is a fork of [claude-sync](https://github.com/tawanorg/claude-sync)
(MIT) adapted to sync `~/.codex` (or `$CODEX_HOME`) rather than claude-sync's
own target directory. Files are gzip-compressed and age-encrypted before
upload; storage is your own bucket (Cloudflare R2, S3, GCS, S3-compatible, or
WebDAV).

## Quick start

    make build && make install          # installs ~/.local/bin/codex-sync
    codex-sync init                     # provider, bucket, passphrase, scope
    codex-sync push                     # first machine
    codex-sync init && codex-sync pull  # second machine, same passphrase
    make install-launchd                # pull+push now, daily at 03:00 and at login

Keep both Macs on the same Codex version: an older engine cannot list threads
written by a newer one.

## What gets synced

| Path (under ~/.codex) | Scope | Notes |
|---|---|---|
| `sessions/`, `archived_sessions/` | sessions | conversations (rollout files; the source of truth) |
| `session_index.jsonl` | sessions | thread names — merged, never overwritten |
| `history.jsonl` | sessions | prompt history — merged, never overwritten |
| `attachments/` | sessions | files you attached to threads |
| `config.toml` | full | providers, MCP servers, project trust |
| `rules/`, `skills/`, `memories/`, `AGENTS.md` | full | |

Never synced: `auth.json`, `installation_id`, every `*.sqlite`/`*.db`, `plugins/`,
`packages/`, `cache/`, logs, worktrees and other runtime state. Codex rebuilds
its databases from the rollout files.

### Sync scope

`init` asks whether to sync everything or just conversation data; set it
directly with `--scope full` or `--scope sessions`:

| Scope | Syncs | Use when |
|---|---|---|
| `full` (default) | everything in the table above | you want config, rules, skills and memories mirrored too |
| `sessions` | `sessions/`, `archived_sessions/`, `session_index.jsonl`, `history.jsonl`, `attachments/` only | you just want conversations to continue across machines |

The scope is saved in `~/.codex-sync/config.yaml` and applies to every
`push`/`pull`; it is also a ceiling on `codex-sync paths add` — a path outside
the current scope is rejected rather than silently widening it.

## How pull behaves

- New and changed remote files are downloaded; a file changed on both sides is
  kept locally and the remote copy saved as `<file>.conflict.<timestamp>`
  (`codex-sync conflicts` resolves them). Sidecars are local only: they are
  never uploaded, tracked or removed by a later pull.
- `session_index.jsonl` and `history.jsonl` are unioned with your local copy.
- A file that vanished from the remote (deleted or archived on the other Mac) is
  moved to `~/.codex-sync/trash/<timestamp>/` when unchanged locally; changed
  files stay. `codex-sync pull --no-delete` disables this; `--dry-run` previews it.
  The trash grows with every removal and nothing references it, so old batches
  are safe to delete.
- An empty remote never removes anything.

## Showing pulled threads in the Codex desktop app

The ChatGPT desktop app keeps its own thread catalog and fills it incrementally:
after a one-time full build it only looks at threads newer than the last one it
saw, so a thread that arrives via sync — whose timestamps are older — never
appears in its sidebar on its own. Quit the ChatGPT app, then either pull with
the refresh built in or run the refresh alone:

```bash
codex-sync pull --desktop      # pull, then refresh the desktop app
codex-sync desktop refresh     # refresh only (after an earlier pull)
```

The refresh backs up the Codex databases to `~/.codex-sync/db-backup-<timestamp>/`,
has the app's own engine index the new rollouts, copies thread names from the
synced `session_index.jsonl` into the engine database (`--no-names` skips this),
and schedules the app's full catalog sweep for its next launch. Relaunch the app
and the pulled threads show up, named. It refuses to run while the ChatGPT app or
any `codex` process is open (they hold the databases; a `codex` running from a
path containing spaces is not detected) — `pull --desktop` still completes the
pull, then reports the running app and exits non-zero, so `pull --desktop && push`
stops there; quit the app and run `codex-sync desktop refresh`. The refresh is
safe to run repeatedly. The engine used is the ChatGPT app's bundled one
(`--codex-bin` / `$CODEX_BIN` override it; a different engine version may migrate
every Codex database, which is why all of them are backed up, except the
engine's log store). Backups accumulate — every run writes a full copy of the
databases, well over 100 MB with a large history — and nothing references them,
so old `db-backup-*` directories are safe to delete.

## How push behaves

Push uploads files whose content changed since the last sync and deletes the
remote copies of files removed locally. A file that still has a live
`.conflict.*` sidecar is skipped and reported as an error until you resolve it
with `codex-sync conflicts`; the sidecar itself is never uploaded.

## Limitations (v1)

- Pulled threads do not appear in the ChatGPT desktop app, and carry no name in
  the CLI, until `codex-sync desktop refresh` (or `pull --desktop`) runs with the
  app quit; Codex itself never re-reads the synced index or rescans older rollouts.
- Do not resume the same thread on two Macs between syncs; you would get a
  conflict sidecar instead of a merged transcript.
- Project organization, automations and the memories database live only in
  SQLite and are not synced.
- macOS only; no npm package — build from source.

## Security

Same model as claude-sync: gzip → age (X25519/ChaCha20-Poly1305); passphrase
keys derived with Argon2id and the fixed salt `sha256("codex-sync-v1")` (so the
same passphrase gives the same key on every device, and a different key than
claude-sync). Config and keys are stored 0600 under `~/.codex-sync/`.

## Setup Guide

### Step 1: Choose a Storage Provider

| Provider | Free Tier | Best For |
|----------|-----------|----------|
| **Cloudflare R2** | 10GB storage | Personal use (recommended) |
| **AWS S3** | 5GB (12 months) | AWS users |
| **Google Cloud Storage** | 5GB | GCP users |
| **S3-compatible** | varies | Backblaze B2, MinIO, Wasabi, DigitalOcean Spaces, self-hosted |
| **WebDAV** | Self-hosted (unlimited) | Nextcloud/ownCloud users |

### Step 2: Create a Bucket

<details>
<summary><b>Cloudflare R2</b> (recommended)</summary>

1. Go to [Cloudflare Dashboard](https://dash.cloudflare.com/) → R2 Object Storage
2. Click "Create bucket" → name it `codex-sync`
3. Go to "Manage R2 API Tokens" → "Create API Token"
4. Select **Object Read & Write** permission → Create

You'll need: Account ID, Access Key ID, Secret Access Key
</details>

<details>
<summary><b>AWS S3</b></summary>

1. Go to [S3 Console](https://s3.console.aws.amazon.com/s3/bucket/create) → Create bucket
2. Go to [IAM Security Credentials](https://console.aws.amazon.com/iam/home#/security_credentials)
3. Create Access Keys

You'll need: Access Key ID, Secret Access Key, Region
</details>

<details>
<summary><b>Google Cloud Storage</b></summary>

1. Go to [Cloud Storage](https://console.cloud.google.com/storage/create-bucket) → Create bucket
2. Go to [Service Accounts](https://console.cloud.google.com/iam-admin/serviceaccounts) → Create service account
3. Grant "Storage Object Admin" role → Create JSON key

You'll need: Project ID, Service Account JSON file (or use `gcloud auth application-default login`)
</details>

<details>
<summary><b>S3-compatible</b> (Backblaze B2, MinIO, Wasabi, DigitalOcean Spaces, ...)</summary>

Any provider exposing an S3-compatible API works through the **S3-compatible (custom endpoint)** option. Create a bucket and an application key with your provider, then supply its S3 endpoint URL.

Example (Backblaze B2):

```bash
codex-sync init --provider s3-compatible --endpoint https://s3.us-west-004.backblazeb2.com
```

You'll need: Endpoint URL, Access Key ID, Secret Access Key, Bucket. The signing region is auto-detected from the endpoint (e.g. `us-west-004`); for providers that ignore it, `auto` is used.

For servers that don't resolve buckets as subdomains (e.g. Ceph RGW, or MinIO without wildcard DNS), add `--use-path-style` to address objects as `endpoint/bucket/key` instead of `bucket.endpoint/key`:

```bash
codex-sync init --provider s3-compatible --endpoint https://ceph.example.com --use-path-style
```

It's off by default and unnecessary for Backblaze B2, Wasabi, and DigitalOcean Spaces, which all support virtual-hosted addressing.

> Custom endpoints automatically relax the AWS SDK's default integrity-checksum headers, which some S3-compatible providers reject. AWS S3 behavior is unchanged.
</details>

<details>
<summary><b>WebDAV (Nextcloud, ownCloud, etc.)</b></summary>

No bucket to create — just point at your existing WebDAV server.

1. **Nextcloud**: Go to Settings → Security → Devices & sessions → Create app password
2. Note your WebDAV URL: `https://your-server/remote.php/dav/files/USERNAME/`

You'll need: WebDAV URL, Username, App password

The wizard will create a `codex-sync` subdirectory automatically.
</details>

### Step 3: Run Init

```bash
codex-sync init
```

The interactive wizard will guide you through:

1. **Select storage provider** (R2, S3, GCS, S3-compatible, or WebDAV)
2. **Enter credentials** (provider-specific)
3. **Choose encryption method**:
   - **Passphrase** (recommended) - same passphrase on all devices = same key
   - **Random key** - must copy `~/.codex-sync/age-key.txt` to other devices
4. **Test the connection** to verify everything works
5. **Choose a sync scope** (`full` or `sessions`, unless `--scope` was given)

### Step 4: Push and Pull

```bash
# Upload local changes
codex-sync push

# Download remote changes
codex-sync pull
```

## Commands

```bash
codex-sync init         # Set up configuration (interactive wizard)
codex-sync push         # Upload local changes to cloud storage
codex-sync pull         # Download remote changes from cloud storage
codex-sync desktop refresh  # Make pulled threads visible in the ChatGPT desktop app
codex-sync status       # Show pending local changes
codex-sync diff         # Show differences between local and remote
codex-sync conflicts    # List and resolve conflicts
codex-sync paths        # Manage sync paths and exclude filters
codex-sync reset        # Remove local config, key and sync state (keeps trash/)
codex-sync update       # Update to latest version (verifies release checksums)
codex-sync changelog    # Show release history
codex-sync --help       # Show all commands
```

### Pull Options

```bash
codex-sync pull                  # Normal pull (prompts if existing files)
codex-sync pull --dry-run        # Preview what would change
codex-sync pull --force          # Skip confirmation prompts
codex-sync pull --no-delete      # Never remove local files that vanished from the remote
```

### Init Options

```bash
codex-sync init                   # Full setup wizard
codex-sync init --passphrase      # Re-enter passphrase only (keeps storage config)
codex-sync init --force           # Reset everything, start fresh
codex-sync init --scope sessions  # Sync conversation data only
```

### Managing Sync Paths

```bash
codex-sync paths                    # List sync paths and exclude filters
codex-sync paths add <path>         # Add a path under ~/.codex to the sync list
codex-sync paths remove <path>      # Remove a path from the sync list
codex-sync paths exclude <glob>     # Skip a glob pattern inside a synced directory
codex-sync paths unexclude <glob>   # Remove a glob filter
codex-sync paths reset              # Restore default sync paths and clear excludes
```

### Quiet Mode

```bash
codex-sync push -q     # No output (for scripts)
codex-sync pull -q
```

Errors are still printed to stderr in quiet mode, and a push or pull with any
failed file exits non-zero, so `pull -q && push -q` stops at the first problem.

### Reset

```bash
codex-sync reset            # Remove ~/.codex-sync/config.yaml, age-key.txt and state.json
codex-sync reset --remote   # Also delete every file in the bucket
codex-sync reset --local    # Kept for compatibility: a plain reset already clears the sync state
```

`reset` never touches `~/.codex` and never touches `~/.codex-sync/trash/`, so
files an earlier pull removed stay recoverable. Run `codex-sync init` afterwards
to set up again.

### Check for Updates

```bash
codex-sync update --check   # Check without installing
codex-sync update           # Download and install latest version
```

There are no published releases yet; both commands print a build-from-source
hint (`git pull && make build && make install`) until the first one exists.

### Changelog

```bash
codex-sync changelog            # Show recent releases
codex-sync changelog --limit 5  # Show last 5 releases
```

## Automatic Sync (launchd)

```bash
make install-launchd     # installs the binary, then the daily agent
make uninstall-launchd   # removes it
```

This installs a per-user launchd agent
(`~/Library/LaunchAgents/com.codex-sync.daily.plist`) that runs
`codex-sync pull -q && codex-sync push -q`. The job runs immediately when
installed (`RunAtLoad`), then daily at 03:00 and at every login. Output goes to
`~/Library/Logs/codex-sync.log`; a failed file makes the job exit non-zero, and
the log says which file and why.

launchd jobs do not see your shell environment. If you use a custom Codex home,
run `CODEX_HOME=/path/to/home make install-launchd` — the value is baked into
the agent at install time (re-run the target to change it). macOS only; there
is no built-in scheduler in the CLI itself.

## Exclude Patterns

Skip specific files or directories during sync by adding exclude patterns to your config (`~/.codex-sync/config.yaml`):

```yaml
exclude:
  - "*.tmp"
  - "attachments/**"
  - "skills/**/node_modules/**"
```

Patterns use glob syntax and are matched against paths relative to `~/.codex`.

## Acceptance check

`integration/codex_listing_check.py` compares the user-visible threads reported
by a real Codex engine binary (the `app-server` protocol) across two
`$CODEX_HOME` directories — a source home and a synced copy — across every
model provider configured in the source home. It needs a local Codex engine
binary, is run manually, and is not part of `make check`.

Run it against copies (or APFS clones) of the homes, never the live `~/.codex`:
the engine writes state into whichever home it is given.

```bash
integration/codex_listing_check.py --source /path/to/copy-of-home \
    --synced /path/to/copy-of-other-home \
    [--codex-bin /Applications/ChatGPT.app/Contents/Resources/codex]
```

It exits 2 with `codex engine binary not found` when the engine cannot be
started (set `--codex-bin` or `CODEX_BIN`).

## Passphrase Issues

### Wrong passphrase on a new device

If you entered the wrong passphrase on a new device:

```bash
# Re-enter passphrase (keeps your storage config)
codex-sync init --passphrase
```

The init will verify your passphrase can decrypt remote files before completing.

### Forgot your passphrase

The passphrase is **never stored**. If you forget it:

1. Your encrypted files cannot be recovered
2. Reset and start fresh:

```bash
codex-sync reset --remote   # Delete remote files and local config/key/state (trash/ kept)
codex-sync init             # Set up again with new passphrase
codex-sync push             # Re-upload from this device
```

## Conflict Resolution

When both local and remote files change, the remote version is saved as `.conflict`:

```bash
codex-sync conflicts            # Interactive resolution
codex-sync conflicts --list     # Just list conflicts
codex-sync conflicts --keep local   # Keep all local versions
codex-sync conflicts --keep remote  # Keep all remote versions
```

Interactive options:
- **[l]** Keep local (delete conflict file)
- **[r]** Keep remote (replace local)
- **[d]** Show diff
- **[s]** Skip
- **[q]** Quit

`session_index.jsonl` and `history.jsonl` never produce conflicts — they are
merged instead (see "How pull behaves").

## Pulling with Existing Files

When you pull on a device that already has `~/.codex` files, codex-sync will:

1. **Show what would change** - files that would be overwritten, kept, merged, or downloaded
2. **Ask for confirmation** - choose to back up, overwrite, or abort
3. **Create a backup** - saves existing files to `~/.codex.backup.<timestamp>`

```bash
# Preview first
codex-sync pull --dry-run

# Pull with prompts
codex-sync pull

# Skip prompts (for scripts)
codex-sync pull --force
```

## Cost

Storage cost depends on how much conversation history you keep — `sessions/`
and `archived_sessions/` scale with usage and can range from a few MB to
several hundred MB on a long-lived install. Even so, it's inexpensive on any
provider:

| Provider | Free Tier |
|----------|-----------|
| **Cloudflare R2** | 10GB storage, 1M writes, 10M reads/month |
| **AWS S3** | 5GB for 12 months (then ~$0.023/GB) |
| **Google Cloud Storage** | 5GB, 5K writes, 50K reads/month |
| **WebDAV** | Self-hosted — no limits, no cost beyond your own server |

## Build from Source

**Prerequisite:** Go 1.24+

```bash
git clone https://github.com/d-jiao/codex-sync
cd codex-sync
make build && make install   # installs ~/.local/bin/codex-sync
codex-sync --version
```

## Development

```bash
make test          # Run tests
make fmt            # Format code
make check           # Run all pre-commit checks
make setup-hooks      # Enable git pre-commit hooks
```

## License

MIT — see [LICENSE](LICENSE). codex-sync is a fork of
[claude-sync](https://github.com/tawanorg/claude-sync); see [NOTICE](NOTICE)
for the fork point and attribution.

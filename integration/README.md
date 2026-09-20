# Integration tests

Tests in this directory run against **real storage** and are gated behind the
`integration` build tag, so `make test` and CI never run them. There are four:

| What | File | Needs |
|---|---|---|
| Two-device sync through a real R2 bucket | `r2_sync_test.go` | R2 credentials (and Docker for the multi-container variant) |
| Whether the endpoint enforces a conditional delete | `conditional_delete_test.go` | R2 (or S3-compatible) credentials |
| Whether the endpoint can copy an object (the recycle bin) | `object_copy_test.go` | R2 (or S3-compatible) credentials |
| Thread-listing comparison between two Codex homes | `codex_listing_check.py` | a local Codex engine binary |

> **Known gap:** `r2_sync_test.go` still writes claude-sync-era fixtures
> (`.claude/CLAUDE.md`, `settings.json`, `projects/`), which are not in the
> Codex sync profile, so its scenarios need to be ported to `.codex` fixtures
> (`AGENTS.md`, `config.toml`, `rules/`, …) before they exercise the current
> profile. Contributions welcome.

## R2 sync test

Use a **dedicated scratch bucket** — the tests clear it — and an API token
scoped to that bucket only.

```bash
export CODEX_SYNC_R2_ACCOUNT_ID=your_account_id
export CODEX_SYNC_R2_ACCESS_KEY_ID=your_access_key
export CODEX_SYNC_R2_SECRET_ACCESS_KEY=your_secret_key
export CODEX_SYNC_R2_BUCKET=codex-sync-test           # default
export CODEX_SYNC_TEST_PASSPHRASE=your-test-passphrase   # default: test-passphrase-123

go test -tags=integration -v ./integration/...
```

Without credentials the tests skip.

### Conditional-delete conformance

`push --force` sends deletes conditional on the object's version, but not every
S3-compatible server enforces `If-Match` on a DELETE; some accept the header and
remove the object anyway. `conditional_delete_test.go` measures what your
endpoint actually does: it uploads a sentinel, asks for a delete conditional on
a version the object never had, and checks whether the object survived. Point it
at each endpoint you sync against, including MinIO or another self-hosted
S3 implementation. A failure here is a property of that server, not a
regression in codex-sync — the version re-read in `push --force` still holds —
but it tells you the second line of defence is absent there.

### Object-copy conformance

Before `push --force` deletes anything it copies the object to
`_trash/<batch>/`, so the copy is the part of the guarantee that has to work.
`object_copy_test.go` exercises the adapter's server-side copy against the real
endpoint with a key containing a space and a `#`, then checks the copy matches
byte for byte and the original is still there. This is the only cover the
S3/R2 `CopyObject` path gets; the in-memory store used by the unit tests cannot
tell you whether a given service accepts the `CopySource` we build. A failure
does not make deletion unsafe — sync falls back to download-then-upload — but
it means every deleted file is paid for twice on the wire.

### Docker variant

`docker-compose.yml` starts three containers (`device-a`, `device-b`,
`device-c`), each with its own home directory, built from
`Dockerfile.test`, and passes the same environment variables through:

```bash
cd integration
docker-compose up --build
```

### Scenarios

1. **Basic cross-device sync** — A: init with passphrase, create files, push.
   B: init with the same passphrase, pull. B has A's files.
2. **Key mismatch detection** — A: init with passphrase 1, push. B: init with
   passphrase 2. `init` detects the mismatch and offers the retry / clear-remote
   / abort choice.
3. **Conflict resolution** — both devices modify the same file; A pushes; B
   pulls and gets a `.conflict.<timestamp>` sidecar with the remote content.
4. **Reset remote and re-push** — with mismatched keys, B runs
   `reset --remote`, `init`, `push`; A pulls and receives B's files.

### Cleanup

The tests clear the bucket when they finish. If a run dies halfway:

```bash
codex-sync reset --remote --force      # with a config pointing at the test bucket

# or with the AWS CLI against the R2 endpoint
aws s3 rm s3://codex-sync-test --recursive \
  --endpoint-url https://<account_id>.r2.cloudflarestorage.com
```

### In GitHub Actions

Store the credentials as repository secrets and map them to the environment:

```yaml
env:
  CODEX_SYNC_R2_ACCOUNT_ID: ${{ secrets.R2_ACCOUNT_ID }}
  CODEX_SYNC_R2_ACCESS_KEY_ID: ${{ secrets.R2_ACCESS_KEY_ID }}
  CODEX_SYNC_R2_SECRET_ACCESS_KEY: ${{ secrets.R2_SECRET_ACCESS_KEY }}
  CODEX_SYNC_R2_BUCKET: codex-sync-ci-test
```

## Thread-listing check

`codex_listing_check.py` is the manual acceptance test for a real sync: it asks
a Codex engine, over the `app-server` protocol, to list the user-visible
threads in a source `$CODEX_HOME` and in a synced copy, across every model
provider configured in the source home, and reports the difference.

Run it against **copies** (or APFS clones) of the homes, never the live
`~/.codex`: the engine writes state into whichever home it is given.

```bash
integration/codex_listing_check.py --source /path/to/copy-of-home \
    --synced /path/to/copy-of-other-home \
    [--codex-bin /Applications/ChatGPT.app/Contents/Resources/codex]
```

Exit codes: `0` same threads, `1` differences found, `2` the engine could not
be started (`codex engine binary not found` — set `--codex-bin` or
`CODEX_BIN`).

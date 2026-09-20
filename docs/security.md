# Security model

What codex-sync protects, what it does not, and the trade-offs behind the
design. The engine is inherited from claude-sync; the inherited third-party
audit and the status of its findings in this fork are in
[../SECURITY-AUDIT.md](../SECURITY-AUDIT.md). To report a vulnerability, see
[../SECURITY.md](../SECURITY.md).

## What leaves the machine

Every file is gzip-compressed and then encrypted with
[age](https://github.com/FiloSottile/age) (X25519 key agreement,
ChaCha20-Poly1305 payload) **before** upload. The remote object is the age
ciphertext; a small manifest of file hashes is uploaded the same way
(`_metadata/manifest.json.age`).

The storage provider can therefore see:

| Visible to the provider | Hidden from the provider |
|---|---|
| object keys — the relative path under `~/.codex` plus `.age` (e.g. `sessions/2026/09/15/rollout-<timestamp>-<uuid>.jsonl.age`) | file contents (conversations, prompts, config, rules, memories) |
| compressed + encrypted object sizes | thread names, prompt history |
| upload timestamps and access patterns | your machine's home directory (rewritten to a `${HOME}` token before encryption) |

Rollout filenames carry a date and a UUID, not a project name or path, so the
key listing reveals *when* you used Codex and how much, but not *what* about.
Attachment filenames under `attachments/` are stored as-is (plus `.age`).

Encryption happens with the *public* half of the key, and decryption with the
*private* half; both live in `~/.codex-sync/age-key.txt`. Anyone who obtains
that file — or the passphrase it was derived from — can read every synced file.

## Keys and passphrases

`codex-sync init` offers two key modes:

- **Passphrase (default).** The X25519 private key is derived with **Argon2id**
  (64 MiB memory, 3 iterations, 4 lanes) from the passphrase and a **fixed
  salt**, `sha256("codex-sync-v1")`. The same passphrase yields the same key on
  every machine, so there is nothing to copy around. The derived key is written
  to `age-key.txt`; the passphrase itself is never stored.
- **Random key.** A fresh age identity with full 256-bit entropy. You copy
  `age-key.txt` to each machine yourself.

Trade-offs of the fixed salt (audit finding M4): it is what makes
passphrase-only setup possible, but it means an attacker who obtains your
ciphertext can attack the passphrase directly, and one precomputation
serves every user. Argon2id's memory hardness slows that down; the **12-character
minimum** enforced by `init` (audit M5) is the other half of the mitigation. Use
a long, unique passphrase or the random-key mode if the data is sensitive.

The salt is deliberately different from claude-sync's (`claude-sync-v1`) so
one passphrase cannot unlock both tools' buckets. **It must never change**:
changing it would silently derive a different key on every existing install.

## What stays local, and how it is protected

| Path | Contents | Permissions |
|---|---|---|
| `~/.codex-sync/config.yaml` | storage provider, bucket, **plaintext storage credentials**, scope, excludes | `0600` |
| `~/.codex-sync/age-key.txt` | the age identity (derived or random) | `0600` |
| `~/.codex-sync/state.json` | per-file hashes, sizes, mtimes, remote versions, sync times | `0600` |
| `~/.codex-sync/trash/`, `db-backup-*/` | files pull removed; database copies from `desktop refresh` | `0700` dirs, `0600` files |
| downloaded files under `~/.codex` | decrypted content | `0700` dirs, `0600` files |
| `~/.codex.backup.<timestamp>` | first-pull backup of pre-existing files, filtered by the configured excludes | `0700` / `0600` |

Storage credentials are stored in plaintext in `config.yaml` (audit M1). The
`0600` mode keeps other local users out, but any process running as you can
read them. Scope the credentials to a single bucket — R2's per-bucket API
tokens and an S3 bucket policy both do this — so a leak exposes only ciphertext
plus the ability to delete or replace it.

Two files under `~/.codex` are **protected**: `auth.json` (your OpenAI login)
and `installation_id` are never uploaded and never written by pull, regardless
of configuration. Every `*.sqlite*` / `*.db*` file is likewise excluded.
The same filter applies to the first-pull backup, so credentials and databases
are not copied into `~/.codex.backup.<timestamp>` either.

## Integrity and availability

- **Tampering.** age authenticates every chunk of ciphertext; a modified or
  truncated object fails to decrypt, and pull reports an error for that file
  while leaving the local copy and its state entry untouched. Object *keys* are
  not authenticated, so someone with
  write access to the bucket could delete objects or move ciphertext between
    keys. Remote keys are validated before writing: a key that would resolve
    outside `~/.codex` is rejected (audit L1).
  Pull additionally refuses any destination path that crosses a symlink, so a
  symlink planted inside `~/.codex` cannot redirect a decrypted file elsewhere
  on disk, and every write is a rename of a temporary file, so an interrupted
  pull cannot leave a half-written file behind.
- **Deletion.** Someone with bucket write access can delete your remote copies.
  Pull moves locally unchanged files whose remote copy vanished into
  `~/.codex-sync/trash/` rather than deleting them, and a pull that finds an
  *empty* bucket removes nothing at all.
  In the other direction, push does not delete remote objects unless you pass
  `--force`, and even then it skips any object another device has replaced
  since your last sync, re-reading each object's version immediately before
  deleting it and sending the delete conditionally where the provider supports
  it. A machine with a stale or partially configured Codex home therefore
  cannot erase everyone else's copy. Not every S3-compatible server enforces
  `If-Match` on a DELETE, so the version re-read — not the header — is the
  guard that has to hold; storage that reports no version leaves only
  timestamps, and push names those deletes in its output.
- **Self-update.** `codex-sync update` downloads a release binary over HTTPS
  from this repository's GitHub Releases and verifies it against the release's
  `checksums.txt` when one is published (audit M2). Releases without checksums
  are installed with a warning.

## Threats this does not address

- A compromised machine: anything that can read `~/.codex-sync/` or your
  running process can read your data.
- A weak passphrase combined with the fixed salt (above).
- Traffic analysis by the storage provider: object sizes, counts and timing
  are visible.
- Metadata in object keys: relative paths and attachment filenames are not
  encrypted.
- Version skew between Codex engines, which can hide threads (a data-visibility
  issue, not a security one, but it looks like data loss).

## Dependencies

Cryptography comes from `filippo.io/age` and `golang.org/x/crypto`
(Argon2id, X25519); storage from the official AWS and Google Cloud SDKs plus a
small WebDAV client built on `net/http`; SQLite access in `desktop refresh`
uses `modernc.org/sqlite` (pure Go, no cgo). Versions are pinned in `go.mod`.

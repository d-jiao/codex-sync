# Security policy

codex-sync encrypts your Codex conversations before they leave your machine.
If you find a way to defeat that — or any other security problem — please tell
us privately first.

## Reporting a vulnerability

Use GitHub's private vulnerability reporting:
**[Report a vulnerability](https://github.com/d-jiao/codex-sync/security/advisories/new)**
(Security tab → *Report a vulnerability*). Please include the version or commit
(`codex-sync --version`), the storage provider, and steps to reproduce.

Please do not open a public issue for security problems. You should hear back
within a week; fixes are published as a normal release with credit to the
reporter unless you prefer otherwise.

## Scope

In scope: anything that lets the storage provider or a third party read synced
content, recover the key or passphrase, tamper with data undetected, or make
codex-sync write outside `~/.codex` / `~/.codex-sync`. See
[docs/security.md](docs/security.md) for the threat model and the accepted
trade-offs (fixed Argon2 salt, plaintext storage credentials in `config.yaml`,
unencrypted object keys) — reports about those known limitations are welcome
as improvement proposals in the issue tracker.

## Supported versions

Only the latest commit on `main` (and the latest release, once releases exist)
receives fixes.

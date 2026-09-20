
## Install

Download the asset for your platform, check it against `checksums.txt`, then put
it on your `PATH`:

```sh
shasum -a 256 --ignore-missing -c checksums.txt   # sha256sum on Linux
chmod +x codex-sync-darwin-arm64
mv codex-sync-darwin-arm64 ~/.local/bin/codex-sync
```

macOS will quarantine a downloaded binary; clear it with
`xattr -d com.apple.quarantine ~/.local/bin/codex-sync`.

An existing install upgrades itself with `codex-sync update`, which downloads the
matching asset from this release and refuses to install it if the checksum does
not match. You can also build from source with `make install`.

#!/bin/bash
set -euo pipefail

# Publish platform-specific npm packages for codex-sync
# Usage: ./scripts/publish-npm.sh <version>
# Expects binaries in bin/ directory (from build-all or CI artifacts)

VERSION="${1:?Usage: publish-npm.sh <version>}"
VERSION="${VERSION#v}" # Strip v prefix

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"
NPM_DIR="$ROOT_DIR/npm"

# Platform packages that failed to publish, reported at the end so one bad
# platform does not hide the others or block the root package. Kept as a plain
# string rather than an array: this script runs under `set -u`, where expanding
# an empty array is an error on some bash versions.
FAILED_PLATFORMS=""

# Map: npm platform → Go binary name
declare -A PLATFORM_MAP=(
  ["darwin-arm64"]="codex-sync-darwin-arm64"
  ["darwin-x64"]="codex-sync-darwin-amd64"
  ["linux-arm64"]="codex-sync-linux-arm64"
  ["linux-x64"]="codex-sync-linux-amd64"
  ["win32-arm64"]="codex-sync-windows-arm64.exe"
  ["win32-x64"]="codex-sync-windows-amd64.exe"
)

# Binary name per platform
declare -A BINARY_NAME=(
  ["darwin-arm64"]="codex-sync"
  ["darwin-x64"]="codex-sync"
  ["linux-arm64"]="codex-sync"
  ["linux-x64"]="codex-sync"
  ["win32-arm64"]="codex-sync.exe"
  ["win32-x64"]="codex-sync.exe"
)

for platform in "${!PLATFORM_MAP[@]}"; do
  src_binary="${PLATFORM_MAP[$platform]}"
  dst_binary="${BINARY_NAME[$platform]}"
  pkg_dir="$NPM_DIR/$platform"

  echo "Publishing @tawandotorg/codex-sync-${platform}@${VERSION}..."

  # Copy binary into package directory
  if [ -f "$ROOT_DIR/bin/$src_binary" ]; then
    cp "$ROOT_DIR/bin/$src_binary" "$pkg_dir/$dst_binary"
    chmod +x "$pkg_dir/$dst_binary"
  elif [ -f "$ROOT_DIR/$src_binary" ]; then
    cp "$ROOT_DIR/$src_binary" "$pkg_dir/$dst_binary"
    chmod +x "$pkg_dir/$dst_binary"
  else
    echo "  WARNING: Binary $src_binary not found, skipping $platform"
    continue
  fi

  # Update version in package.json
  cd "$pkg_dir"
  npm version "$VERSION" --no-git-tag-version --allow-same-version 2>/dev/null

  # A platform publish must not abort the run. The root package carries a
  # postinstall that downloads the binary from GitHub Releases, so it is still
  # worth publishing even when a platform package fails — users fall back
  # instead of getting nothing. Failures are collected and re-raised at the end
  # so CI still goes red.
  if npm publish --access public; then
    echo "  Published!"
  else
    echo "  ERROR: failed to publish @tawandotorg/codex-sync-${platform}@${VERSION}"
    FAILED_PLATFORMS="$FAILED_PLATFORMS $platform"
  fi

  # Clean up binary (don't commit binaries)
  rm -f "$pkg_dir/$dst_binary"
done

# Publish main package
echo "Publishing @tawandotorg/codex-sync@${VERSION}..."
cd "$ROOT_DIR"

# Update optionalDependencies versions
for platform in "${!PLATFORM_MAP[@]}"; do
  sed -i.bak "s|\"@tawandotorg/codex-sync-${platform}\": \".*\"|\"@tawandotorg/codex-sync-${platform}\": \"${VERSION}\"|" package.json
done
rm -f package.json.bak

npm version "$VERSION" --no-git-tag-version --allow-same-version 2>/dev/null

ROOT_FAILED=0
if npm publish --access public; then
  echo "Published @tawandotorg/codex-sync@${VERSION}!"
else
  echo "ERROR: failed to publish @tawandotorg/codex-sync@${VERSION}"
  ROOT_FAILED=1
fi

if [ -n "$FAILED_PLATFORMS" ]; then
  echo
  echo "Platform packages that failed to publish:$FAILED_PLATFORMS"
  echo "A 404 on PUT for a package that has never been published usually means"
  echo "NPM_TOKEN cannot create new packages in the @tawandotorg scope — grant"
  echo "the token read/write on the whole scope, not just existing packages."
fi

if [ -n "$FAILED_PLATFORMS" ] || [ "$ROOT_FAILED" -eq 1 ]; then
  exit 1
fi

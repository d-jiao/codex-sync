#!/bin/bash
# Check if codex-sync is configured and return status as additionalContext

CONFIG_FILE="$HOME/.codex-sync/config.yaml"
KEY_FILE="$HOME/.codex-sync/age-key.txt"

if ! command -v codex-sync &> /dev/null; then
    echo "codex-sync is not installed. Install with: npm install -g @tawandotorg/codex-sync"
    exit 0
fi

if [ ! -f "$CONFIG_FILE" ]; then
    echo "codex-sync is not configured. Run /sync-init to set up cross-device sync."
    exit 0
fi

if [ ! -f "$KEY_FILE" ]; then
    echo "codex-sync encryption key not found. Run /sync-init to configure."
    exit 0
fi

# Check if there are pending changes
STATUS=$(codex-sync status -q 2>/dev/null)
if [ $? -eq 0 ] && [ -n "$STATUS" ]; then
    COUNT=$(echo "$STATUS" | wc -l | tr -d ' ')
    echo "codex-sync: $COUNT file(s) pending. Use /sync-push to upload or /sync-pull to download."
fi

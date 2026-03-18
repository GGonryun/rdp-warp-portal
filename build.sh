#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

echo "=== Building agent (Windows amd64) ==="
cd "$SCRIPT_DIR/agent"
GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o "$SCRIPT_DIR/agent/agent.exe" ./cmd/p0rtal-agent
echo "  -> agent/agent.exe ($(du -h "$SCRIPT_DIR/agent/agent.exe" | cut -f1))"

echo "=== Done ==="

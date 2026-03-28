#!/bin/bash
# Contrabass launcher — sets up environment and runs the orchestrator
# Usage: ./run.sh --config /path/to/WORKFLOW.md [additional contrabass flags]

set -euo pipefail

export GITHUB_TOKEN="${GITHUB_TOKEN:-$(gh auth token 2>/dev/null)}"

if [ -z "$GITHUB_TOKEN" ]; then
    echo "ERROR: GITHUB_TOKEN not set and gh auth token failed" >&2
    exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
exec "$SCRIPT_DIR/contrabass" "$@"

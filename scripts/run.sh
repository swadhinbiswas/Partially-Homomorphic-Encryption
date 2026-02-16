#!/bin/bash
set -euo pipefail

CONFIG="${1:-configs/default.json}"

echo "=== PHE Experiment Environment ==="
echo "Config: $CONFIG"
echo ""

# Build
echo "Building..."
go build -o bin/experiment ./cmd/usersim
echo "Build complete."

# Run
echo "Starting experiments..."
./bin/experiment "$CONFIG"

echo ""
echo "Done. Results stored in data/"

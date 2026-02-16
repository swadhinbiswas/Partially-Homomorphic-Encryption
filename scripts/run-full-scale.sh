#!/bin/bash
set -euo pipefail

CONFIG="${1:-configs/default.json}"
LOG_DIR="${2:-logs}"

if [[ ! -f "$CONFIG" ]]; then
  echo "ERROR: config file not found: $CONFIG" >&2
  exit 1
fi

if ! command -v go >/dev/null 2>&1; then
  echo "ERROR: Go is not installed or not in PATH." >&2
  echo "Install Go 1.22+ and rerun." >&2
  exit 1
fi

mkdir -p bin "$LOG_DIR"
mkdir -p data

TS="$(date +%Y%m%d-%H%M%S)"
LOG_FILE="$LOG_DIR/full-scale-$TS.log"

echo "=============================================================="
echo " Full-Scale PHE Run"
echo " Config    : $CONFIG"
echo " Log file  : $LOG_FILE"
echo " Start time: $(date -Iseconds)"
echo "=============================================================="

echo "[1/6] Downloading Go modules (if any)..."
go mod download

echo "[2/6] Refreshing proxy list (best effort)..."
if [[ -x scripts/update-proxies.sh ]]; then
  if ! bash scripts/update-proxies.sh; then
    echo "WARN: proxy refresh script failed; runtime will attempt fresh download from config URL."
  fi
else
  echo "WARN: scripts/update-proxies.sh not executable/present; skipping manual refresh."
fi

echo "[3/6] Building experiment binary..."
go build -o bin/experiment ./cmd/usersim

echo "[4/6] Running full-scale simulation..."
echo "Press Ctrl+C to stop early."
./bin/experiment "$CONFIG" 2>&1 | tee "$LOG_FILE"

echo "[5/6] Aggregating summaries..."
go run ./cmd/aggregate data | tee -a "$LOG_FILE"

echo "[6/6] Done."
echo "End time: $(date -Iseconds)"
echo "Outputs:"
echo "  - Raw data   : data/"
echo "  - Analysis   : data/analysis/"
echo "  - Run log    : $LOG_FILE"


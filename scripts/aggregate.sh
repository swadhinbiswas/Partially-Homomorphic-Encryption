#!/bin/bash
set -euo pipefail

DATA_DIR="${1:-data}"

echo "Aggregating experiment summaries under: $DATA_DIR"
go run ./cmd/aggregate "$DATA_DIR"
echo "Done. See $DATA_DIR/analysis/"


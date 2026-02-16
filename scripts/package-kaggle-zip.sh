#!/bin/bash
set -euo pipefail

# Usage:
#   bash scripts/package-kaggle-zip.sh [data_dir] [dataset_dir] [zip_path]
#
# Example:
#   bash scripts/package-kaggle-zip.sh data kaggle_dataset kaggle_dataset.zip

DATA_DIR="${1:-data}"
DATASET_DIR="${2:-kaggle_dataset}"
ZIP_PATH="${3:-kaggle_dataset.zip}"

if [[ ! -d "$DATA_DIR" ]]; then
  echo "ERROR: data directory not found: $DATA_DIR" >&2
  exit 1
fi

if [[ ! -x "scripts/prepare-kaggle-dataset.sh" ]]; then
  echo "ERROR: scripts/prepare-kaggle-dataset.sh is missing or not executable." >&2
  exit 1
fi

echo "[1/3] Building Kaggle dataset folder (full mode)..."
bash scripts/prepare-kaggle-dataset.sh full "$DATA_DIR" "$DATASET_DIR"

echo "[2/3] Creating zip archive: $ZIP_PATH"
rm -f "$ZIP_PATH"

python3 - "$DATASET_DIR" "$ZIP_PATH" <<'PY'
import os
import sys
import zipfile

src_dir = sys.argv[1]
zip_path = sys.argv[2]

with zipfile.ZipFile(zip_path, "w", compression=zipfile.ZIP_DEFLATED) as zf:
    for root, _, files in os.walk(src_dir):
        for name in files:
            abs_path = os.path.join(root, name)
            rel_path = os.path.relpath(abs_path, src_dir)
            zf.write(abs_path, rel_path)

print(f"Created {zip_path}")
PY

echo "[3/3] Done."
echo "Zip ready: $ZIP_PATH"
if command -v du >/dev/null 2>&1; then
  du -sh "$ZIP_PATH" | awk '{print "Size: " $1}'
fi


#!/bin/bash
set -euo pipefail

# Usage:
#   bash scripts/prepare-kaggle-dataset.sh [summary|research|full] [data_dir] [output_dir]
#
# Modes:
#   summary (default): include analysis tables + per-run meta/summary files
#   research: include summary + raw chunk CSV files
#   full: include everything in summary + raw chunk CSV/JSONL files, separated into folders

MODE="${1:-full}"
DATA_DIR="${2:-data}"
OUT_DIR="${3:-kaggle_dataset}"

if [[ "$MODE" != "summary" && "$MODE" != "research" && "$MODE" != "full" ]]; then
  echo "ERROR: mode must be 'summary', 'research', or 'full' (got: $MODE)" >&2
  exit 1
fi

if [[ ! -d "$DATA_DIR" ]]; then
  echo "ERROR: data directory not found: $DATA_DIR" >&2
  exit 1
fi

if [[ ! -f "go.mod" ]]; then
  echo "ERROR: run this from repo root (go.mod not found)." >&2
  exit 1
fi

echo "[1/6] Rebuilding analysis summaries..."
go run ./cmd/aggregate "$DATA_DIR"

if [[ ! -f "$DATA_DIR/analysis/summary_by_run.csv" || ! -f "$DATA_DIR/analysis/summary_aggregated.csv" ]]; then
  echo "ERROR: expected analysis CSV files were not generated." >&2
  exit 1
fi

echo "[2/6] Preparing output directory: $OUT_DIR"
rm -rf "$OUT_DIR"
mkdir -p "$OUT_DIR"/{analysis,runs,raw_csv,raw_jsonl,config,samples}

echo "[3/6] Copying analysis artifacts..."
cp "$DATA_DIR/analysis/summary_by_run.csv" "$OUT_DIR/analysis/"
cp "$DATA_DIR/analysis/summary_aggregated.csv" "$OUT_DIR/analysis/"

echo "[4/6] Copying run metadata..."
while IFS= read -r summary_file; do
  run_dir="$(dirname "$summary_file")"
  rel_summary="${summary_file#"$DATA_DIR"/}"
  dst_summary="$OUT_DIR/runs/$rel_summary"
  mkdir -p "$(dirname "$dst_summary")"
  cp "$summary_file" "$dst_summary"

  meta_file="$run_dir/meta.json"
  if [[ -f "$meta_file" ]]; then
    rel_meta="${meta_file#"$DATA_DIR"/}"
    dst_meta="$OUT_DIR/runs/$rel_meta"
    mkdir -p "$(dirname "$dst_meta")"
    cp "$meta_file" "$dst_meta"
  fi
done < <(find "$DATA_DIR" -type f -name "summary.json" | sort)

if [[ "$MODE" == "research" || "$MODE" == "full" ]]; then
  echo "[4b/6] Copying raw request chunks (${MODE} mode)..."
  while IFS= read -r summary_file; do
    run_dir="$(dirname "$summary_file")"
    while IFS= read -r chunk_file; do
      rel="${chunk_file#"$DATA_DIR"/}"
      dst="$OUT_DIR/raw_csv/$rel"
      mkdir -p "$(dirname "$dst")"
      cp "$chunk_file" "$dst"
    done < <(find "$run_dir" -maxdepth 1 -type f -name "chunk_*.csv" | sort)
    if [[ "$MODE" == "full" ]]; then
      while IFS= read -r jsonl_file; do
        rel="${jsonl_file#"$DATA_DIR"/}"
        dst="$OUT_DIR/raw_jsonl/$rel"
        mkdir -p "$(dirname "$dst")"
        cp "$jsonl_file" "$dst"
      done < <(find "$run_dir" -maxdepth 1 -type f -name "chunk_*.jsonl" | sort)
    fi
  done < <(find "$DATA_DIR" -type f -name "summary.json" | sort)
fi

echo "[5/6] Writing documentation and metadata templates..."
cp configs/default.json "$OUT_DIR/config/default.json"

RUNS_COUNT=$(( $(wc -l < "$OUT_DIR/analysis/summary_by_run.csv") - 1 ))
EXPERIMENT_COUNT=$(( $(wc -l < "$OUT_DIR/analysis/summary_aggregated.csv") - 1 ))
TOTAL_COMPLETED=$(awk -F, 'NR>1 {s+=$4} END {printf "%.0f", s}' "$OUT_DIR/analysis/summary_aggregated.csv")
TOTAL_FAILED=$(awk -F, 'NR>1 {s+=$5} END {printf "%.0f", s}' "$OUT_DIR/analysis/summary_aggregated.csv")
GENERATED_AT="$(date -Iseconds)"

cat > "$OUT_DIR/README.md" <<EOF
# homomorphic_request

Kaggle dataset id: \`swadhinbiswas/homomorphic_request\`

This dataset contains high-resolution benchmark data from a Paillier-based privacy-preserving API gateway simulation.
It is designed for performance analysis, reliability studies, and ML modeling.

## Snapshot
- Generated at: ${GENERATED_AT}
- Package mode: ${MODE}
- Experiment groups: ${EXPERIMENT_COUNT}
- Total runs: ${RUNS_COUNT}
- Total completed requests: ${TOTAL_COMPLETED}
- Total failed requests: ${TOTAL_FAILED}

## Contents
- \`analysis/summary_by_run.csv\`: one row per run.
- \`analysis/summary_aggregated.csv\`: grouped summary by experiment and operation mode.
- \`runs/**/meta.json\`: per-run configuration snapshot.
- \`runs/**/summary.json\`: per-run counters and correctness/retry stats.
- \`samples/sample_request_rows.csv\`: small sample of request-level CSV rows.
- \`samples/sample_request_rows.jsonl\`: small sample of request-level JSONL rows.
- \`config/default.json\`: configuration used to generate these runs.
EOF

if [[ "$MODE" == "research" || "$MODE" == "full" ]]; then
cat >> "$OUT_DIR/README.md" <<'EOF'
- `raw_csv/**/chunk_*.csv`: per-request records (tabular format).
EOF
fi

if [[ "$MODE" == "full" ]]; then
cat >> "$OUT_DIR/README.md" <<'EOF'
- `raw_jsonl/**/chunk_*.jsonl`: per-request records (JSON Lines format).
EOF
fi

cat >> "$OUT_DIR/README.md" <<'EOF'

## Dataset Structure
```
analysis/
  summary_by_run.csv
  summary_aggregated.csv
runs/
  <experiment_name>/
    <run_id>/
      meta.json
      summary.json
raw_csv/
  <experiment_name>/<run_id>/chunk_*.csv
raw_jsonl/
  <experiment_name>/<run_id>/chunk_*.jsonl
config/
  default.json
samples/
  sample_request_rows.csv
  sample_request_rows.jsonl
DATA_DICTIONARY.md
dataset-metadata.json
UPLOAD_INSTRUCTIONS.md
```

## Notes
- `operation` / `operation_mode` values:
  - `sum`: homomorphic sum over encrypted values
  - `count`: encrypted count operation
  - `mixed`: both operations in a run
- Failures are labeled using `failure_stage` and `failure_code`.
- Correctness validation is sampled (`correctness_sampled`).
- `raw_csv` and `raw_jsonl` contain the same request-level observations in different formats.

## Recommended Starting Files
1. Use `analysis/summary_aggregated.csv` for quick overview.
2. Use `analysis/summary_by_run.csv` for run-level comparisons.
3. Use `raw_csv/**/chunk_*.csv` for model training and detailed analysis.

## Suggested ML Targets
- Regression: `end_to_end_latency_ms`
- Classification: `success` / `failure_stage` / `failure_code`
- QA/validation: `correctness_ok` (for sampled rows)
EOF

cat > "$OUT_DIR/DATA_DICTIONARY.md" <<'EOF'
# Data Dictionary

## Per-request columns (`chunk_*.csv`)
- `run_id`: unique run identifier.
- `experiment_name`: experiment label (e.g., `concurrency_100_users`).
- `operation`: request operation (`sum` or `count`).
- `request_id`: unique request identifier.
- `timestamp`: RFC3339 timestamp.
- `user_id`: simulated user/goroutine id.
- `batch_size`: encrypted values per request.
- `plaintext_size_bytes`: pre-encryption payload size.
- `ciphertext_size_bytes`: serialized encrypted request size.
- `encryption_time_ms`: client encryption latency.
- `marshal_time_ms`: request JSON marshal time.
- `http_roundtrip_ms`: HTTP roundtrip time.
- `response_unmarshal_ms`: response unmarshal time.
- `proxy_delay_ms`: configured proxy delay.
- `gateway_processing_ms`: gateway total processing time.
- `gateway_parse_ms`: gateway request parse time.
- `gateway_enqueue_ms`: time blocked enqueueing to worker queue.
- `gateway_encode_ms`: response encode time.
- `phe_compute_ms`: worker homomorphic compute time.
- `queue_wait_ms`: worker queue wait time.
- `end_to_end_latency_ms`: full client-observed latency.
- `http_status_code`: HTTP status code.
- `attempt_count`: number of attempts used for the request.
- `retried`: whether request was retried.
- `success`: request success flag.
- `failure_stage`: failure stage label.
- `failure_code`: normalized failure code.
- `correctness_sampled`: whether correctness was sampled.
- `expected_plain_result`: expected plaintext result (sampled only).
- `decrypted_result`: decrypted gateway result (sampled only).
- `correctness_ok`: sampled correctness result.
- `error_message`: raw error message on failure.

## Run-level summary (`analysis/summary_by_run.csv`)
- `run_id`, `experiment_name`, `operation_mode`
- `completed`, `failed`, `retries`
- `success_rate`
- `correctness_sampled`, `correctness_passed`, `correctness_failed`
- `correctness_pass_rate`
- `run_dir`

## Experiment-level summary (`analysis/summary_aggregated.csv`)
- `experiment_name`, `operation_mode`, `runs`
- `total_completed`, `total_failed`
- `overall_success_rate`, `mean_success_rate`
- `total_retries`, `mean_retries_per_run`
- `total_correctness_sampled`, `total_correctness_passed`, `total_correctness_failed`
- `overall_correctness_pass_rate`
EOF

cat > "$OUT_DIR/dataset-metadata.json" <<'EOF'
{
  "title": "homomorphic_request",
  "id": "swadhinbiswas/homomorphic_request",
  "licenses": [
    {
      "name": "CC-BY-4.0"
    }
  ],
  "subtitle": "Benchmark data for a Paillier-based privacy-preserving API gateway",
  "description": "Per-request and run-level benchmark data from a homomorphic encryption API gateway simulation. Includes latency, queueing, retry, failure taxonomy, and correctness sampling fields.",
  "keywords": [
    "homomorphic encryption",
    "paillier",
    "performance benchmark",
    "privacy",
    "distributed systems"
  ]
}
EOF

# Sample rows for quick inspection in Kaggle
SAMPLE_CSV_SRC="$(find "$OUT_DIR/raw_csv" -type f -name 'chunk_*.csv' | sort | head -n 1 || true)"
if [[ -n "$SAMPLE_CSV_SRC" && -f "$SAMPLE_CSV_SRC" ]]; then
  { head -n 101 "$SAMPLE_CSV_SRC"; } > "$OUT_DIR/samples/sample_request_rows.csv"
fi

SAMPLE_JSONL_SRC="$(find "$OUT_DIR/raw_jsonl" -type f -name 'chunk_*.jsonl' | sort | head -n 1 || true)"
if [[ -n "$SAMPLE_JSONL_SRC" && -f "$SAMPLE_JSONL_SRC" ]]; then
  { head -n 100 "$SAMPLE_JSONL_SRC"; } > "$OUT_DIR/samples/sample_request_rows.jsonl"
fi

cat > "$OUT_DIR/UPLOAD_INSTRUCTIONS.md" <<'EOF'
# Kaggle Upload Instructions

1. Install and configure Kaggle API:
```bash
pip install kaggle
```
Place your Kaggle API token at `~/.kaggle/kaggle.json` and run:
```bash
chmod 600 ~/.kaggle/kaggle.json
```

2. (Optional) Edit `dataset-metadata.json`:
- current id is set to `swadhinbiswas/homomorphic_request`
- adjust title/subtitle/license if needed

3. Create dataset:
```bash
kaggle datasets create -p .
```

4. Create a new version later:
```bash
kaggle datasets version -p . -m "update: new runs"
```
EOF

echo "[6/6] Done."
echo "Kaggle-ready package created at: $OUT_DIR"
echo "Mode: $MODE"

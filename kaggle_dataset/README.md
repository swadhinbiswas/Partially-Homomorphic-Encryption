# homomorphic_request

Kaggle dataset id: `swadhinbiswas/homomorphic_request`

This dataset contains high-resolution benchmark data from a Paillier-based privacy-preserving API gateway simulation.
It is designed for performance analysis, reliability studies, and ML modeling.

## Snapshot
- Generated at: 2026-02-16T13:50:01+06:00
- Package mode: full
- Experiment groups: 17
- Total runs: 34
- Total completed requests: 426015
- Total failed requests: 19

## Contents
- `analysis/summary_by_run.csv`: one row per run.
- `analysis/summary_aggregated.csv`: grouped summary by experiment and operation mode.
- `runs/**/meta.json`: per-run configuration snapshot.
- `runs/**/summary.json`: per-run counters and correctness/retry stats.
- `samples/sample_request_rows.csv`: small sample of request-level CSV rows.
- `samples/sample_request_rows.jsonl`: small sample of request-level JSONL rows.
- `config/default.json`: configuration used to generate these runs.
- `raw_csv/**/chunk_*.csv`: per-request records (tabular format).
- `raw_jsonl/**/chunk_*.jsonl`: per-request records (JSON Lines format).

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

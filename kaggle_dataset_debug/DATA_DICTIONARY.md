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

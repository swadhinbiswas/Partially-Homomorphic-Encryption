# Privacy-First REST API Gateway Using Partially Homomorphic Encryption

**An experimental environment for empirically evaluating the viability, performance, and scalability of Paillier-based encrypted REST API gateways under realistic network conditions.**

---

## Table of Contents

1. [Architecture Overview](#architecture-overview)
2. [Prerequisites](#prerequisites)
3. [Repository Structure](#repository-structure)
4. [Quick Start](#quick-start)
5. [Running the Server (Gateway + Proxy)](#running-the-server)
6. [Running the Simulation](#running-the-simulation)
7. [Configuration Reference](#configuration-reference)
8. [Proxy Pool Setup](#proxy-pool-setup)
9. [Experiments](#experiments)
10. [Output Format](#output-format)
11. [Scaling to 1 Billion Requests](#scaling-to-1-billion-requests)
12. [Key Management](#key-management)
13. [Reproducibility](#reproducibility)
14. [Troubleshooting](#troubleshooting)

---

## Architecture Overview

```
                    ┌─────────────────────────┐
                    │  Concurrent User         │
                    │  Simulation Engine       │
                    │  (N goroutines)          │
                    │                         │
                    │  • Generate payloads     │
                    │  • Paillier encrypt      │
                    │  • Measure latency       │
                    └────────┬────────────────┘
                             │ HTTP POST (encrypted JSON)
                             ▼
                    ┌─────────────────────────┐
                    │  Real External Proxy     │  ← Optional: 1000+ HTTP/SOCKS5/SOCKS4 proxies
                    │  Pool (round-robin)      │    from iplocate/free-proxy-list
                    └────────┬────────────────┘
                             │
                             ▼
                    ┌─────────────────────────┐
                    │  Untrusted Proxy         │  ← Port 8081 (built-in)
                    │  Simulator               │
                    │                         │
                    │  • Latency injection     │
                    │  • Fault injection       │
                    │  • Metadata observation  │
                    │  • Honest-but-curious    │
                    └────────┬────────────────┘
                             │
                             ▼
                    ┌─────────────────────────┐
                    │  Privacy-First REST      │  ← Port 8082
                    │  API Gateway             │
                    │                         │
                    │  • No plaintext access   │
                    │  • Stateless             │
                    │  • Routes to workers     │
                    └────────┬────────────────┘
                             │
                             ▼
                    ┌─────────────────────────┐
                    │  PHE Worker Pool         │
                    │  (Go channels)           │
                    │                         │
                    │  • Homomorphic Sum       │
                    │  • Homomorphic Count     │
                    │  • Queue wait tracking   │
                    │  • Compute time tracking │
                    └─────────────────────────┘
```

All components run in a **single Go process**. Zero external dependencies.

---

## Prerequisites

- **Go 1.22+** (tested on Go 1.25.6)
- Linux, macOS, or Windows
- No third-party Go modules required (pure standard library)

Verify your Go installation:

```bash
go version
```

---

## Repository Structure

```
homomorphic/
├── cmd/
│   └── usersim/
│       └── main.go              # Entry point — starts everything
├── internal/
│   ├── crypto/
│   │   └── paillier/
│   │       └── paillier.go      # Paillier PHE (keygen, encrypt, decrypt, add, mul)
│   ├── gateway/
│   │   └── gateway.go           # REST API gateway (sum, count operations)
│   ├── metrics/
│   │   └── metrics.go           # Streaming CSV+JSONL recorder (chunked, disk-only)
│   ├── proxy/
│   │   └── proxy.go             # L7 reverse proxy simulator
│   ├── usersim/
│   │   └── simulator.go         # Concurrent user simulation engine
│   └── worker/
│       └── worker.go            # PHE worker pool (Go channels)
├── configs/
│   ├── default.json             # Experiment configuration
│   └── proxies.txt              # External proxy pool (1000+ proxies)
├── scripts/
│   ├── run.sh                   # Build and run
│   └── update-proxies.sh        # Fetch fresh proxies from GitHub
├── data/                        # Output directory (created at runtime)
├── go.mod
└── README.md
```

---

## Quick Start

Run everything with one command:

```bash
go run cmd/usersim/main.go
```

This single command:
1. Generates a Paillier key pair (1024-bit)
2. Starts the REST API gateway on port 8082
3. Starts the proxy simulator on port 8081
4. Loads 1000+ external proxies from `configs/proxies.txt`
5. Runs all 5 experiment suites sequentially
6. Streams all results to `data/` as CSV + JSONL

---

## Running the Server

The gateway and proxy are **embedded** — they start automatically when you launch the experiment. There is no separate server binary to run.

### What starts automatically

| Component | Port | Role |
|---|---|---|
| REST API Gateway | `8082` | Receives encrypted requests, routes to PHE workers |
| Proxy Simulator | `8081` | L7 reverse proxy with fault/latency injection |

### Verifying the server is running

While the experiment is running, you can check the gateway health:

```bash
curl http://localhost:8082/health
# Response: ok
```

### Sending a manual request (for debugging)

```bash
# The gateway expects encrypted payloads — this tests the endpoint is reachable
curl -X POST http://localhost:8082/compute \
  -H "Content-Type: application/json" \
  -d '{"request_id":"test","operation":"sum","payloads":[]}'
```

### Changing ports

Edit `configs/default.json`:

```json
{
    "gateway_port": 8082,
    "proxy_port": 8081
}
```

---

## Running the Simulation

### Method 1: Direct Go run

```bash
# Uses configs/default.json if present, otherwise built-in defaults
go run cmd/usersim/main.go

# Specify a custom config file
go run cmd/usersim/main.go configs/default.json

# Use a completely custom config
go run cmd/usersim/main.go configs/my-experiment.json
```

### Method 2: Build and run (recommended for long experiments)

```bash
bash scripts/run.sh
# or
bash scripts/run.sh configs/my-experiment.json
```

### Method 3: Manual build

```bash
go build -o bin/experiment ./cmd/usersim
./bin/experiment configs/default.json
```

### What happens during a run

```
================================================================
  Privacy-First REST API Gateway — PHE Experiment Environment
================================================================
Config: key_bits=1024 workers=16 queue=10000 data=data chunk=1000000 seed=42
Generating Paillier key pair (1024 bits)...
Key pair generated.
Gateway starting on :8082
Proxy starting on :8081 → gateway :8082
[PROXY POOL] Loaded 1025 proxies (HTTP/S:410 SOCKS5:546 SOCKS4:69) from configs/proxies.txt
Proxy pool auto-refresh: every 30 minutes from configs/proxies.txt
Services ready.
═══════ EXPERIMENT: Concurrency Scaling ═══════
>>> [concurrency_1_users] starting
  [SIM] Proxy pool: 1025 real proxies (round-robin per user)
  [SIM] start: users=1 batch=10 expected=100 duration=0s proxies=1025
    [concurrency_1_users] completed=23 failed=0 rate=4.6/s elapsed=5.0s
    ...
<<< [concurrency_1_users] done: 100 records in 21.3s
>>> [concurrency_10_users] starting
    ...
================================================================
  ALL EXPERIMENTS COMPLETE
  Total records : 165100
  Total time    : 14m32s
  Data directory: data/
================================================================
```

### Stopping a run

Press `Ctrl+C` to stop. Data written so far is safely flushed to disk.

---

## Configuration Reference

All configuration is in `configs/default.json`:

### Global Settings

| Field | Type | Default | Description |
|---|---|---|---|
| `key_bits` | int | `1024` | Paillier key size in bits. Higher = more secure but slower. |
| `gateway_port` | int | `8082` | HTTP port for the REST API gateway |
| `proxy_port` | int | `8081` | HTTP port for the built-in proxy simulator |
| `forward_proxy_url` | string | `""` | Single external forward proxy (e.g., `http://squid:3128`) |
| `proxy_pool_file` | string | `"configs/proxies.txt"` | Path to proxy pool file (one proxy per line) |
| `proxy_refresh_minutes` | int | `30` | Auto-reload proxy file interval (0 = disabled) |
| `worker_count` | int | `16` | Number of PHE worker goroutines |
| `worker_queue_size` | int | `10000` | Buffered channel size for worker job queue |
| `data_dir` | string | `"data"` | Output directory for experiment results |
| `chunk_records` | int | `1000000` | Max records per CSV/JSONL chunk file |
| `seed` | int | `42` | Deterministic random seed for reproducibility |
| `http_timeout_seconds` | int | `60` | HTTP client timeout per request |

### Experiment Configuration

Each experiment block can be `enabled: true/false` independently.

#### Concurrency Scaling

```json
"concurrency_scaling": {
    "enabled": true,
    "levels": [1, 10, 100, 500, 1000],
    "batch_size": 10,
    "requests_per_user": 100,
    "proxy_latency_ms": 0,
    "proxy_drop_rate": 0.0
}
```

#### Batch Size Scaling

```json
"batch_scaling": {
    "enabled": true,
    "concurrency": 10,
    "batch_sizes": [1, 10, 100, 1000],
    "requests_per_user": 100,
    "proxy_latency_ms": 0,
    "proxy_drop_rate": 0.0
}
```

#### Proxy Latency Impact

```json
"latency_impact": {
    "enabled": true,
    "concurrency": 10,
    "batch_size": 10,
    "latencies_ms": [0, 10, 50, 100],
    "requests_per_user": 100,
    "proxy_drop_rate": 0.0
}
```

#### Throughput Stress Test

```json
"throughput": {
    "enabled": true,
    "concurrency": 1000,
    "batch_size": 10,
    "duration_seconds": 30,
    "proxy_latency_ms": 0,
    "proxy_drop_rate": 0.0
}
```

#### Failure Injection

```json
"failure_injection": {
    "enabled": true,
    "concurrency": 50,
    "batch_size": 10,
    "drop_rates": [0.0, 0.01, 0.05],
    "requests_per_user": 100,
    "proxy_latency_ms": 0
}
```

---

## Proxy Pool Setup

### Built-in pool (1000+ proxies)

The file `configs/proxies.txt` ships with 1,025 verified proxies:
- 410 HTTP/HTTPS proxies
- 546 SOCKS5 proxies
- 69 SOCKS4 proxies

### Updating the proxy pool

Fetch the latest proxies from [iplocate/free-proxy-list](https://github.com/iplocate/free-proxy-list) (updated every 30 minutes):

```bash
bash scripts/update-proxies.sh
```

### Auto-refresh during experiments

Set in `configs/default.json`:

```json
"proxy_refresh_minutes": 30
```

The system reloads `configs/proxies.txt` from disk every 30 minutes while running. Run `update-proxies.sh` in a cron job alongside the experiment:

```bash
# Cron: refresh proxies every 30 minutes
*/30 * * * * cd /path/to/homomorphic && bash scripts/update-proxies.sh
```

### Adding your own proxies

Edit `configs/proxies.txt` — one proxy per line:

```
# HTTP proxies
http://185.235.16.12:80
https://103.1.50.198:3125

# SOCKS5 proxies
socks5://68.1.210.163:4145

# SOCKS4 proxies (attempted, may fail — failures recorded as data)
socks4://186.1.181.2:60606

# Plain IP:PORT defaults to http://
203.190.117.137:8123
```

### Disabling external proxies

Set `proxy_pool_file` to empty string:

```json
"proxy_pool_file": ""
```

---

## Experiments

### Experiment 1: Concurrency Scaling

**Question**: How does the system perform as concurrent users increase?

| Parameter | Values |
|---|---|
| Concurrent users | 1, 10, 100, 500, 1000 |
| Batch size | 10 |
| Proxy latency | 0 ms |

**Output**: `data/concurrency_{N}_users/`

### Experiment 2: Batch Size Scaling

**Question**: How does batch size affect encryption overhead and gateway throughput?

| Parameter | Values |
|---|---|
| Batch sizes | 1, 10, 100, 1000 |
| Concurrent users | 10 |
| Proxy latency | 0 ms |

**Output**: `data/batch_{N}/`

### Experiment 3: Proxy Latency Impact

**Question**: How does network latency affect end-to-end performance?

| Parameter | Values |
|---|---|
| Latencies | 0, 10, 50, 100 ms |
| Concurrent users | 10 |
| Batch size | 10 |

**Output**: `data/latency_{N}ms/`

### Experiment 4: Throughput Stress Test

**Question**: What is the maximum sustained throughput under burst load?

| Parameter | Values |
|---|---|
| Concurrent users | 1000 |
| Duration | 30 seconds |
| Batch size | 10 |

**Output**: `data/throughput_burst/`

### Experiment 5: Failure Injection

**Question**: How does the system behave under network failures?

| Parameter | Values |
|---|---|
| Drop rates | 0%, 1%, 5% |
| Concurrent users | 50 |
| Batch size | 10 |

**Output**: `data/failure_drop_{N}pct/`

---

## Output Format

Results are streamed directly to disk — never held in memory.

### Directory structure

```
data/
├── concurrency_1_users/
│   ├── chunk_0000.csv         # Columnar data (for pandas, R, etc.)
│   ├── chunk_0000.jsonl       # JSON Lines (one JSON object per line)
│   └── meta.json              # Experiment parameters
├── concurrency_10_users/
│   └── ...
├── batch_1/
│   └── ...
├── latency_0ms/
│   └── ...
├── throughput_burst/
│   └── ...
└── failure_drop_0pct/
    └── ...
```

### CSV columns (14 fields per record)

| Column | Type | Description |
|---|---|---|
| `request_id` | string | Unique identifier `{experiment}-u{user}-r{seq}` |
| `timestamp` | string | RFC3339Nano timestamp |
| `user_id` | int | Simulated user goroutine ID |
| `batch_size` | int | Number of ciphertexts in this request |
| `plaintext_size_bytes` | int | Equivalent plaintext payload size |
| `ciphertext_size_bytes` | int | Actual encrypted JSON body size |
| `encryption_time_ms` | float | Client-side Paillier encryption time |
| `proxy_delay_ms` | float | Configured proxy latency |
| `gateway_processing_ms` | float | Total gateway processing time |
| `phe_compute_ms` | float | PHE worker computation time |
| `queue_wait_ms` | float | Time waiting in worker queue |
| `end_to_end_latency_ms` | float | Total client-observed latency |
| `success` | bool | Request success/failure |
| `error_message` | string | Error details (empty if success) |

### Chunked files

Files rotate at `chunk_records` (default: 1,000,000 records per file):
- `chunk_0000.csv`, `chunk_0001.csv`, `chunk_0002.csv`, ...
- At 1B records: 1,000 chunk files per experiment

---

## Scaling to 1 Billion Requests

The system is designed for incremental, long-running execution:

1. **Streaming writes**: Records go directly to disk via buffered channel
2. **Chunked files**: Auto-rotate at 1M records per file
3. **No memory accumulation**: Only a 50,000-record channel buffer exists
4. **Progress reporting**: Logs rate and count every 5 seconds

### Example: 1B records across concurrency scaling

```json
{
    "concurrency_scaling": {
        "enabled": true,
        "levels": [1, 10, 100, 500, 1000, 5000, 10000],
        "batch_size": 10,
        "requests_per_user": 100000
    }
}
```

Total: `(1+10+100+500+1000+5000+10000) * 100000 = 1,661,100,000 requests`

### Estimated runtime

With 1024-bit Paillier on an 8-core machine:
- ~100-500 requests/second (depending on batch size and concurrency)
- 1B requests at 300 req/s ~ 39 days

Run on a server, use `nohup` or `tmux`:

```bash
nohup ./bin/experiment configs/default.json > experiment.log 2>&1 &
```

---

## Key Management

| Key | Location | Purpose |
|---|---|---|
| Private key | User simulator only | Decryption (never leaves client) |
| Public key | Gateway + workers | Encryption + homomorphic operations |

- Keys are generated fresh at startup (deterministic with same seed)
- No external KMS
- No hardcoded keys
- Key ID mapping is implicit (single key pair per experiment run)

---

## Reproducibility

- **Deterministic seeds**: Each user goroutine gets `seed + userID`
- **Configuration preserved**: `meta.json` saved per experiment directory
- **Single-command execution**: `go run cmd/usersim/main.go`
- **No external services**: Everything runs in-process
- **No third-party dependencies**: Pure Go standard library
- **No mock cryptography**: Real Paillier encryption with `crypto/rand`

---

## Troubleshooting

### Port already in use

```
FATAL: gateway: listen tcp :8082: bind: address already in use
```

Change ports in `configs/default.json` or kill the existing process:

```bash
lsof -ti:8082 | xargs kill -9
```

### Proxy connection failures

Proxy failures are **expected and recorded as data**. Dead proxies produce failure records with error messages in the CSV/JSONL output. This is realistic experimental data.

### SOCKS4 proxy errors

Go's standard library supports HTTP, HTTPS, and SOCKS5 proxies natively. SOCKS4 proxies will fail with connection errors — these failures are recorded as data points showing protocol reliability differences.

### Out of file descriptors (high concurrency)

```bash
ulimit -n 65536
```

### Slow key generation

1024-bit Paillier key generation takes 1-3 seconds. For faster testing, use `"key_bits": 512` (not cryptographically secure, but faster for development).

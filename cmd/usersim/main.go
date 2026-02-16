package main

import (
	"bufio"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"homomorphic/internal/crypto/paillier"
	"homomorphic/internal/gateway"
	"homomorphic/internal/metrics"
	"homomorphic/internal/proxy"
	"homomorphic/internal/usersim"
)

// ---------------------------------------------------------------------------
// Configuration structs — loaded from JSON, or built-in defaults are used.
// ---------------------------------------------------------------------------

type ExperimentConfig struct {
	KeyBits                     int              `json:"key_bits"`
	GatewayPort                 int              `json:"gateway_port"`
	ProxyPort                   int              `json:"proxy_port"`
	ForwardProxyURL             string           `json:"forward_proxy_url"`
	ProxyPoolFile               string           `json:"proxy_pool_file"`
	ProxyPoolURL                string           `json:"proxy_pool_url"`
	ProxyDownloadTimeoutSeconds int              `json:"proxy_download_timeout_seconds"`
	ProxyRefreshMinutes         int              `json:"proxy_refresh_minutes"`
	WorkerCount                 int              `json:"worker_count"`
	WorkerQueueSize             int              `json:"worker_queue_size"`
	DataDir                     string           `json:"data_dir"`
	ChunkRecords                int64            `json:"chunk_records"`
	Seed                        int64            `json:"seed"`
	HTTPTimeoutSeconds          int              `json:"http_timeout_seconds"`
	OperationMode               string           `json:"operation_mode"`
	CorrectnessSampleRate       float64          `json:"correctness_sample_rate"`
	RetryMaxAttempts            int              `json:"retry_max_attempts"`
	RetryBackoffMS              int              `json:"retry_backoff_ms"`
	RunsPerExperiment           int              `json:"runs_per_experiment"`
	Experiments                 ExperimentsBlock `json:"experiments"`

	// Loaded at runtime (not from JSON)
	proxyMgr *proxyPoolManager `json:"-"`
}

type ExperimentsBlock struct {
	ConcurrencyScaling *ConcurrencyExp `json:"concurrency_scaling"`
	BatchScaling       *BatchExp       `json:"batch_scaling"`
	LatencyImpact      *LatencyExp     `json:"latency_impact"`
	Throughput         *ThroughputExp  `json:"throughput"`
	FailureInjection   *FailureExp     `json:"failure_injection"`
}

type ConcurrencyExp struct {
	Enabled         bool    `json:"enabled"`
	Levels          []int   `json:"levels"`
	BatchSize       int     `json:"batch_size"`
	RequestsPerUser int     `json:"requests_per_user"`
	ProxyLatencyMS  int     `json:"proxy_latency_ms"`
	ProxyDropRate   float64 `json:"proxy_drop_rate"`
}

type BatchExp struct {
	Enabled         bool    `json:"enabled"`
	Concurrency     int     `json:"concurrency"`
	BatchSizes      []int   `json:"batch_sizes"`
	RequestsPerUser int     `json:"requests_per_user"`
	ProxyLatencyMS  int     `json:"proxy_latency_ms"`
	ProxyDropRate   float64 `json:"proxy_drop_rate"`
}

type LatencyExp struct {
	Enabled         bool    `json:"enabled"`
	Concurrency     int     `json:"concurrency"`
	BatchSize       int     `json:"batch_size"`
	LatenciesMS     []int   `json:"latencies_ms"`
	RequestsPerUser int     `json:"requests_per_user"`
	ProxyDropRate   float64 `json:"proxy_drop_rate"`
}

type ThroughputExp struct {
	Enabled         bool    `json:"enabled"`
	Concurrency     int     `json:"concurrency"`
	BatchSize       int     `json:"batch_size"`
	DurationSeconds int     `json:"duration_seconds"`
	ProxyLatencyMS  int     `json:"proxy_latency_ms"`
	ProxyDropRate   float64 `json:"proxy_drop_rate"`
}

type FailureExp struct {
	Enabled         bool      `json:"enabled"`
	Concurrency     int       `json:"concurrency"`
	BatchSize       int       `json:"batch_size"`
	DropRates       []float64 `json:"drop_rates"`
	RequestsPerUser int       `json:"requests_per_user"`
	ProxyLatencyMS  int       `json:"proxy_latency_ms"`
}

// ---------------------------------------------------------------------------
// Defaults — used when config file is absent or partially filled.
// ---------------------------------------------------------------------------

func defaultConfig() *ExperimentConfig {
	return &ExperimentConfig{
		KeyBits:                     1024,
		GatewayPort:                 8082,
		ProxyPort:                   8081,
		ProxyPoolURL:                "https://cdn.jsdelivr.net/gh/proxifly/free-proxy-list@main/proxies/all/data.txt",
		ProxyDownloadTimeoutSeconds: 30,
		WorkerCount:                 16,
		WorkerQueueSize:             10000,
		DataDir:                     "data",
		ChunkRecords:                1_000_000,
		Seed:                        42,
		HTTPTimeoutSeconds:          60,
		OperationMode:               "sum",
		CorrectnessSampleRate:       0.0,
		RetryMaxAttempts:            1,
		RetryBackoffMS:              0,
		RunsPerExperiment:           1,
		Experiments: ExperimentsBlock{
			ConcurrencyScaling: &ConcurrencyExp{
				Enabled:         true,
				Levels:          []int{1, 10, 100, 500, 1000},
				BatchSize:       10,
				RequestsPerUser: 100,
			},
			BatchScaling: &BatchExp{
				Enabled:         true,
				Concurrency:     10,
				BatchSizes:      []int{1, 10, 100, 1000},
				RequestsPerUser: 100,
			},
			LatencyImpact: &LatencyExp{
				Enabled:         true,
				Concurrency:     10,
				BatchSize:       10,
				LatenciesMS:     []int{0, 10, 50, 100},
				RequestsPerUser: 100,
			},
			Throughput: &ThroughputExp{
				Enabled:         true,
				Concurrency:     1000,
				BatchSize:       10,
				DurationSeconds: 30,
			},
			FailureInjection: &FailureExp{
				Enabled:         true,
				Concurrency:     50,
				BatchSize:       10,
				DropRates:       []float64{0, 0.01, 0.05},
				RequestsPerUser: 100,
			},
		},
	}
}

func loadConfig(path string) (*ExperimentConfig, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var cfg ExperimentConfig
	if err := json.NewDecoder(f).Decode(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	log.Println("================================================================")
	log.Println("  Privacy-First REST API Gateway — PHE Experiment Environment")
	log.Println("================================================================")

	// --- Load config ---
	configPath := "configs/default.json"
	if len(os.Args) > 1 {
		configPath = os.Args[1]
	}

	cfg, err := loadConfig(configPath)
	if err != nil {
		log.Printf("Config %s not loaded (%v), using built-in defaults", configPath, err)
		cfg = defaultConfig()
	}

	log.Printf("Config: key_bits=%d workers=%d queue=%d data=%s chunk=%d seed=%d",
		cfg.KeyBits, cfg.WorkerCount, cfg.WorkerQueueSize,
		cfg.DataDir, cfg.ChunkRecords, cfg.Seed)
	if cfg.ForwardProxyURL != "" {
		log.Printf("Config: forward_proxy_url=%s (all traffic routed through single proxy)", cfg.ForwardProxyURL)
	}

	// --- Load proxy pool (with optional auto-refresh) ---
	if cfg.ProxyPoolFile != "" {
		timeout := time.Duration(cfg.ProxyDownloadTimeoutSeconds) * time.Second
		if cfg.ProxyDownloadTimeoutSeconds <= 0 {
			timeout = 30 * time.Second
		}
		cfg.proxyMgr = newProxyPoolManager(cfg.ProxyPoolFile, cfg.ProxyPoolURL, timeout)
		cfg.proxyMgr.refresh()
		if cfg.ProxyRefreshMinutes > 0 {
			cfg.proxyMgr.startAutoRefresh(time.Duration(cfg.ProxyRefreshMinutes) * time.Minute)
			log.Printf("Proxy pool auto-refresh: every %d minutes from %s", cfg.ProxyRefreshMinutes, cfg.ProxyPoolFile)
		}
	}

	// --- Key generation ---
	log.Printf("Generating Paillier key pair (%d bits)...", cfg.KeyBits)
	privKey, err := paillier.GenerateKey(cfg.KeyBits)
	if err != nil {
		log.Fatalf("FATAL: key generation failed: %v", err)
	}
	log.Println("Key pair generated. Public key available to gateway; private key stays with simulator.")

	// --- Start gateway ---
	gw := gateway.NewGateway(cfg.GatewayPort, cfg.WorkerCount, cfg.WorkerQueueSize, &privKey.PublicKey)
	go func() {
		if err := gw.Start(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("FATAL: gateway: %v", err)
		}
	}()
	log.Printf("Gateway starting on :%d", cfg.GatewayPort)

	// --- Start proxy ---
	p, err := proxy.New(proxy.Config{
		TargetURL: fmt.Sprintf("http://localhost:%d", cfg.GatewayPort),
		Port:      cfg.ProxyPort,
	})
	if err != nil {
		log.Fatalf("FATAL: proxy creation: %v", err)
	}
	go func() {
		if err := p.Start(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("FATAL: proxy: %v", err)
		}
	}()
	log.Printf("Proxy starting on :%d → gateway :%d", cfg.ProxyPort, cfg.GatewayPort)

	// --- Wait for readiness ---
	waitForHealth(fmt.Sprintf("http://localhost:%d/health", cfg.GatewayPort))
	log.Println("Services ready.")

	// --- Ensure data dir ---
	os.MkdirAll(cfg.DataDir, 0755)

	simBase := fmt.Sprintf("http://localhost:%d", cfg.ProxyPort)
	timeout := time.Duration(cfg.HTTPTimeoutSeconds) * time.Second

	totalStart := time.Now()
	var totalRecords int64

	// ========================  EXPERIMENTS  ========================

	if e := cfg.Experiments.ConcurrencyScaling; e != nil && e.Enabled {
		totalRecords += runConcurrencyScaling(cfg, e, privKey, p, simBase, timeout)
	}

	if e := cfg.Experiments.BatchScaling; e != nil && e.Enabled {
		totalRecords += runBatchScaling(cfg, e, privKey, p, simBase, timeout)
	}

	if e := cfg.Experiments.LatencyImpact; e != nil && e.Enabled {
		totalRecords += runLatencyImpact(cfg, e, privKey, p, simBase, timeout)
	}

	if e := cfg.Experiments.Throughput; e != nil && e.Enabled {
		totalRecords += runThroughput(cfg, e, privKey, p, simBase, timeout)
	}

	if e := cfg.Experiments.FailureInjection; e != nil && e.Enabled {
		totalRecords += runFailureInjection(cfg, e, privKey, p, simBase, timeout)
	}

	// ========================  SUMMARY  ========================

	log.Println("================================================================")
	log.Println("  ALL EXPERIMENTS COMPLETE")
	log.Printf("  Total records : %d", totalRecords)
	log.Printf("  Total time    : %v", time.Since(totalStart))
	log.Printf("  Data directory: %s/", cfg.DataDir)
	log.Println("================================================================")
}

// ---------------------------------------------------------------------------
// Health check with retry
// ---------------------------------------------------------------------------

func waitForHealth(url string) {
	for i := 0; i < 100; i++ {
		resp, err := http.Get(url)
		if err == nil && resp.StatusCode == 200 {
			resp.Body.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	log.Fatal("FATAL: services did not become healthy within 5 s")
}

// ---------------------------------------------------------------------------
// Experiment helper: create recorder → run simulator → close → return count
// ---------------------------------------------------------------------------

func runExperiment(
	name string,
	cfg *ExperimentConfig,
	privKey *paillier.PrivateKey,
	simCfg usersim.Config,
	simBase string,
	timeout time.Duration,
) int64 {
	log.Printf(">>> [%s] starting", name)
	runID := newRunID()
	dataDir := filepath.Join(cfg.DataDir, name, runID)
	recorder, err := metrics.NewStreamRecorder(dataDir, cfg.ChunkRecords)
	if err != nil {
		log.Printf("ERROR: recorder for %s: %v", name, err)
		return 0
	}

	// Save experiment metadata alongside the data
	simCfg.RunID = runID
	simCfg.ProxyURL = simBase
	simCfg.Seed = cfg.Seed
	simCfg.OperationMode = cfg.OperationMode
	simCfg.CorrectnessSampleRate = cfg.CorrectnessSampleRate
	simCfg.RetryMaxAttempts = cfg.RetryMaxAttempts
	simCfg.RetryBackoffMS = cfg.RetryBackoffMS
	simCfg.ForwardProxyURL = cfg.ForwardProxyURL
	if cfg.proxyMgr != nil {
		simCfg.ProxyPoolFunc = cfg.proxyMgr.get
	}
	saveMeta(dataDir, simCfg, cfg.Seed, cfg.KeyBits)

	sim := usersim.New(simCfg, privKey, recorder, timeout)
	start := time.Now()
	sim.Run()
	recorder.Close()
	saveSummary(dataDir, sim.Stats())

	n := recorder.Total()
	log.Printf("<<< [%s] done: %d records in %v", name, n, time.Since(start))
	return n
}

func runWithRepeats(
	name string,
	cfg *ExperimentConfig,
	privKey *paillier.PrivateKey,
	simCfg usersim.Config,
	simBase string,
	timeout time.Duration,
) int64 {
	repeats := cfg.RunsPerExperiment
	if repeats < 1 {
		repeats = 1
	}

	var total int64
	for i := 1; i <= repeats; i++ {
		if repeats > 1 {
			log.Printf("  [%s] run %d/%d", name, i, repeats)
		}
		total += runExperiment(name, cfg, privKey, simCfg, simBase, timeout)
	}
	return total
}

func saveMeta(dir string, simCfg usersim.Config, seed int64, keyBits int) {
	meta := map[string]interface{}{
		"run_id":                  simCfg.RunID,
		"experiment_name":         simCfg.ExperimentName,
		"num_users":               simCfg.NumUsers,
		"batch_size":              simCfg.BatchSize,
		"requests_per_user":       simCfg.RequestsPerUser,
		"duration":                simCfg.Duration.String(),
		"operation_mode":          simCfg.OperationMode,
		"correctness_sample_rate": simCfg.CorrectnessSampleRate,
		"retry_max_attempts":      simCfg.RetryMaxAttempts,
		"retry_backoff_ms":        simCfg.RetryBackoffMS,
		"key_bits":                keyBits,
		"proxy_delay_ms":          simCfg.ProxyDelayMS,
		"forward_proxy_url":       simCfg.ForwardProxyURL,
		"proxy_pool_size":         len(simCfg.CurrentPool()),
		"seed":                    seed,
		"started_at":              time.Now().Format(time.RFC3339),
	}
	f, err := os.Create(filepath.Join(dir, "meta.json"))
	if err != nil {
		return
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	enc.Encode(meta)
}

func saveSummary(dir string, stats usersim.Stats) {
	f, err := os.Create(filepath.Join(dir, "summary.json"))
	if err != nil {
		return
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	_ = enc.Encode(stats)
}

func newRunID() string {
	b := make([]byte, 8)
	if _, err := crand.Read(b); err != nil {
		return fmt.Sprintf("run-%d", time.Now().UnixNano())
	}
	return "run-" + hex.EncodeToString(b)
}

// ---------------------------------------------------------------------------
// Proxy pool manager — thread-safe, auto-refreshing
// ---------------------------------------------------------------------------

// proxyPoolManager holds a live proxy list that can be reloaded from disk.
// Supports auto-refresh (e.g., every 30 min when the upstream source updates).
type proxyPoolManager struct {
	mu              sync.RWMutex
	proxies         []string
	file            string
	sourceURL       string
	downloadTimeout time.Duration
}

func newProxyPoolManager(file, sourceURL string, downloadTimeout time.Duration) *proxyPoolManager {
	return &proxyPoolManager{
		file:            file,
		sourceURL:       sourceURL,
		downloadTimeout: downloadTimeout,
	}
}

// get returns a snapshot of the current proxy list (thread-safe).
func (m *proxyPoolManager) get() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cp := make([]string, len(m.proxies))
	copy(cp, m.proxies)
	return cp
}

// reload reads proxies from the file on disk.
func (m *proxyPoolManager) reload() {
	proxies, err := loadProxyFile(m.file)
	if err != nil {
		log.Printf("[PROXY POOL] reload failed: %v", err)
		return
	}
	m.mu.Lock()
	m.proxies = proxies
	m.mu.Unlock()

	var httpCount, socks5Count, socks4Count, otherCount int
	for _, p := range proxies {
		switch {
		case strings.HasPrefix(p, "http://") || strings.HasPrefix(p, "https://"):
			httpCount++
		case strings.HasPrefix(p, "socks5://"):
			socks5Count++
		case strings.HasPrefix(p, "socks4://"):
			socks4Count++
		default:
			otherCount++
		}
	}
	log.Printf("[PROXY POOL] Loaded %d proxies (HTTP/S:%d SOCKS5:%d SOCKS4:%d other:%d) from %s",
		len(proxies), httpCount, socks5Count, socks4Count, otherCount, m.file)
}

// refresh downloads a fresh proxy list (if sourceURL is set), then reloads from disk.
func (m *proxyPoolManager) refresh() {
	if m.sourceURL != "" {
		n, err := downloadProxyPool(m.sourceURL, m.file, m.downloadTimeout)
		if err != nil {
			log.Printf("[PROXY POOL] download failed (using existing file if present): %v", err)
		} else {
			log.Printf("[PROXY POOL] downloaded %d fresh proxies from %s", n, m.sourceURL)
		}
	}
	m.reload()
}

// startAutoRefresh reloads the proxy file at the given interval.
func (m *proxyPoolManager) startAutoRefresh(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			m.refresh()
		}
	}()
}

// loadProxyFile reads a text file with one proxy per line.
// Supports: http://IP:PORT, socks5://IP:PORT, socks4://IP:PORT, IP:PORT (defaults to http://)
func loadProxyFile(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var proxies []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.Contains(line, "://") {
			line = "http://" + line
		}
		proxies = append(proxies, line)
	}
	return proxies, scanner.Err()
}

// downloadProxyPool fetches a plaintext proxy list and atomically writes it to targetPath.
func downloadProxyPool(sourceURL, targetPath string, timeout time.Duration) (int, error) {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(sourceURL)
	if err != nil {
		return 0, fmt.Errorf("fetch %s: %w", sourceURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("fetch %s: status %d", sourceURL, resp.StatusCode)
	}

	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		return 0, fmt.Errorf("mkdir for %s: %w", targetPath, err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(targetPath), "proxies-*.tmp")
	if err != nil {
		return 0, fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		return 0, fmt.Errorf("write temp proxy file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return 0, fmt.Errorf("close temp proxy file: %w", err)
	}

	f, err := os.Open(tmpPath)
	if err != nil {
		return 0, fmt.Errorf("reopen temp proxy file: %w", err)
	}
	defer f.Close()

	var count int
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		count++
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("scan temp proxy file: %w", err)
	}
	if count == 0 {
		return 0, fmt.Errorf("downloaded proxy list is empty")
	}

	if err := os.Rename(tmpPath, targetPath); err != nil {
		return 0, fmt.Errorf("move proxy file into place: %w", err)
	}
	return count, nil
}

// ---------------------------------------------------------------------------
// Experiment 1: Concurrency Scaling
// ---------------------------------------------------------------------------

func runConcurrencyScaling(
	cfg *ExperimentConfig, e *ConcurrencyExp,
	privKey *paillier.PrivateKey, p *proxy.Proxy,
	simBase string, timeout time.Duration,
) int64 {
	log.Println("═══════ EXPERIMENT: Concurrency Scaling ═══════")
	p.UpdateConfig(proxy.Config{
		LatencyFixed:    time.Duration(e.ProxyLatencyMS) * time.Millisecond,
		DropProbability: e.ProxyDropRate,
	})

	var total int64
	for _, level := range e.Levels {
		name := fmt.Sprintf("concurrency_%d_users", level)
		total += runWithRepeats(name, cfg, privKey, usersim.Config{
			NumUsers:        level,
			BatchSize:       e.BatchSize,
			RequestsPerUser: e.RequestsPerUser,
			ProxyDelayMS:    float64(e.ProxyLatencyMS),
			ExperimentName:  name,
		}, simBase, timeout)
	}
	return total
}

// ---------------------------------------------------------------------------
// Experiment 2: Batch Size Scaling
// ---------------------------------------------------------------------------

func runBatchScaling(
	cfg *ExperimentConfig, e *BatchExp,
	privKey *paillier.PrivateKey, p *proxy.Proxy,
	simBase string, timeout time.Duration,
) int64 {
	log.Println("═══════ EXPERIMENT: Batch Size Scaling ═══════")
	p.UpdateConfig(proxy.Config{
		LatencyFixed:    time.Duration(e.ProxyLatencyMS) * time.Millisecond,
		DropProbability: e.ProxyDropRate,
	})

	var total int64
	for _, batchSize := range e.BatchSizes {
		name := fmt.Sprintf("batch_%d", batchSize)
		total += runWithRepeats(name, cfg, privKey, usersim.Config{
			NumUsers:        e.Concurrency,
			BatchSize:       batchSize,
			RequestsPerUser: e.RequestsPerUser,
			ProxyDelayMS:    float64(e.ProxyLatencyMS),
			ExperimentName:  name,
		}, simBase, timeout)
	}
	return total
}

// ---------------------------------------------------------------------------
// Experiment 3: Proxy Latency Impact
// ---------------------------------------------------------------------------

func runLatencyImpact(
	cfg *ExperimentConfig, e *LatencyExp,
	privKey *paillier.PrivateKey, p *proxy.Proxy,
	simBase string, timeout time.Duration,
) int64 {
	log.Println("═══════ EXPERIMENT: Proxy Latency Impact ═══════")

	var total int64
	for _, latMS := range e.LatenciesMS {
		p.UpdateConfig(proxy.Config{
			LatencyFixed:    time.Duration(latMS) * time.Millisecond,
			DropProbability: e.ProxyDropRate,
		})

		name := fmt.Sprintf("latency_%dms", latMS)
		total += runWithRepeats(name, cfg, privKey, usersim.Config{
			NumUsers:        e.Concurrency,
			BatchSize:       e.BatchSize,
			RequestsPerUser: e.RequestsPerUser,
			ProxyDelayMS:    float64(latMS),
			ExperimentName:  name,
		}, simBase, timeout)
	}
	return total
}

// ---------------------------------------------------------------------------
// Experiment 4: Throughput Stress Test
// ---------------------------------------------------------------------------

func runThroughput(
	cfg *ExperimentConfig, e *ThroughputExp,
	privKey *paillier.PrivateKey, p *proxy.Proxy,
	simBase string, timeout time.Duration,
) int64 {
	log.Println("═══════ EXPERIMENT: Throughput Stress Test ═══════")
	p.UpdateConfig(proxy.Config{
		LatencyFixed:    time.Duration(e.ProxyLatencyMS) * time.Millisecond,
		DropProbability: e.ProxyDropRate,
	})

	name := "throughput_burst"
	return runWithRepeats(name, cfg, privKey, usersim.Config{
		NumUsers:       e.Concurrency,
		BatchSize:      e.BatchSize,
		Duration:       time.Duration(e.DurationSeconds) * time.Second,
		ProxyDelayMS:   float64(e.ProxyLatencyMS),
		ExperimentName: name,
	}, simBase, timeout)
}

// ---------------------------------------------------------------------------
// Experiment 5: Failure Injection
// ---------------------------------------------------------------------------

func runFailureInjection(
	cfg *ExperimentConfig, e *FailureExp,
	privKey *paillier.PrivateKey, p *proxy.Proxy,
	simBase string, timeout time.Duration,
) int64 {
	log.Println("═══════ EXPERIMENT: Failure Injection ═══════")

	var total int64
	for _, dropRate := range e.DropRates {
		p.UpdateConfig(proxy.Config{
			LatencyFixed:    time.Duration(e.ProxyLatencyMS) * time.Millisecond,
			DropProbability: dropRate,
		})

		name := fmt.Sprintf("failure_drop_%dpct", int(dropRate*100))
		total += runWithRepeats(name, cfg, privKey, usersim.Config{
			NumUsers:        e.Concurrency,
			BatchSize:       e.BatchSize,
			RequestsPerUser: e.RequestsPerUser,
			ProxyDelayMS:    float64(e.ProxyLatencyMS),
			ExperimentName:  name,
		}, simBase, timeout)
	}
	return total
}

package usersim

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"homomorphic/internal/crypto/paillier"
	"homomorphic/internal/metrics"
)

// Config controls one experiment run.
type Config struct {
	ProxyURL              string // target URL e.g. "http://localhost:8081"
	NumUsers              int    // concurrent goroutines
	BatchSize             int    // ciphertexts per request
	RunID                 string
	OperationMode         string  // "sum", "count", or "mixed"
	CorrectnessSampleRate float64 // [0,1]
	RetryMaxAttempts      int
	RetryBackoffMS        int
	RequestsPerUser       int           // fixed-count mode (set >0)
	Duration              time.Duration // duration mode (set >0); overrides RequestsPerUser
	ProxyDelayMS          float64       // configured delay, recorded in metrics
	ExperimentName        string
	Seed                  int64
	ForwardProxyURL       string          // single external forward proxy (e.g. "http://squid:3128")
	ProxyPool             []string        // static proxy pool (used if ProxyPoolFunc is nil)
	ProxyPoolFunc         func() []string // dynamic proxy pool (auto-refreshed); overrides ProxyPool
}

// Simulator drives concurrent encrypted traffic through real proxies to the gateway.
type Simulator struct {
	config   Config
	privKey  *paillier.PrivateKey
	pubKey   *paillier.PublicKey
	recorder *metrics.StreamRecorder
	timeout  time.Duration

	completed  atomic.Int64
	failed     atomic.Int64
	reqCounter atomic.Int64
	retries    atomic.Int64
	stop       atomic.Bool

	mu                 sync.Mutex
	failureStageCounts map[string]int64
	failureCodeCounts  map[string]int64
	operationCounts    map[string]int64
	correctnessSampled int64
	correctnessPassed  int64
	correctnessFailed  int64
}

// New creates a simulator. The recorder must already be open.
func New(cfg Config, privKey *paillier.PrivateKey, recorder *metrics.StreamRecorder, timeout time.Duration) *Simulator {
	pool := cfg.CurrentPool()
	if len(pool) > 0 {
		log.Printf("  [SIM] Proxy pool: %d real proxies (round-robin per user)", len(pool))
	} else if cfg.ForwardProxyURL != "" {
		log.Printf("  [SIM] Single forward proxy: %s", cfg.ForwardProxyURL)
	} else {
		log.Printf("  [SIM] Direct connection (no external proxy)")
	}

	return &Simulator{
		config:             cfg,
		privKey:            privKey,
		pubKey:             &privKey.PublicKey,
		recorder:           recorder,
		timeout:            timeout,
		failureStageCounts: map[string]int64{},
		failureCodeCounts:  map[string]int64{},
		operationCounts:    map[string]int64{},
	}
}

// CurrentPool returns the live proxy pool — dynamic if available, else static.
func (c *Config) CurrentPool() []string {
	if c.ProxyPoolFunc != nil {
		return c.ProxyPoolFunc()
	}
	return c.ProxyPool
}

// makeClient builds an HTTP client for a specific simulated user.
// Uses Transport.Proxy as a function so each request picks from the
// latest proxy pool (supports auto-refresh while experiment is running).
func (s *Simulator) makeClient(uid int) *http.Client {
	transport := &http.Transport{
		MaxIdleConns:        10,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     120 * time.Second,
		Proxy:               s.proxySelector(uid),
	}

	return &http.Client{
		Timeout:   s.timeout,
		Transport: transport,
	}
}

// proxySelector returns a function that picks a proxy for this user.
// Called per-request, so pool refreshes take effect immediately.
func (s *Simulator) proxySelector(uid int) func(*http.Request) (*url.URL, error) {
	return func(r *http.Request) (*url.URL, error) {
		// Priority 1: proxy pool (static or dynamic)
		pool := s.config.CurrentPool()
		if len(pool) > 0 {
			addr := pool[uid%len(pool)]
			return url.Parse(addr)
		}

		// Priority 2: single forward proxy
		if s.config.ForwardProxyURL != "" {
			return url.Parse(s.config.ForwardProxyURL)
		}

		// Priority 3: direct connection
		return nil, nil
	}
}

// Run starts all simulated users and blocks until they finish.
func (s *Simulator) Run() {
	totalExpected := "unlimited"
	if s.config.Duration == 0 {
		totalExpected = fmt.Sprintf("%d", s.config.NumUsers*s.config.RequestsPerUser)
	}
	poolSize := len(s.config.CurrentPool())
	log.Printf("  [SIM] start: users=%d batch=%d expected=%s duration=%v proxies=%d",
		s.config.NumUsers, s.config.BatchSize, totalExpected, s.config.Duration, poolSize)

	var wg sync.WaitGroup
	done := make(chan struct{})
	go s.reportProgress(done)

	if s.config.Duration > 0 {
		// Duration-based mode: run for a fixed wall-clock period
		time.AfterFunc(s.config.Duration, func() {
			s.stop.Store(true)
		})
		for i := 0; i < s.config.NumUsers; i++ {
			wg.Add(1)
			go func(uid int) {
				defer wg.Done()
				rng := rand.New(rand.NewSource(s.config.Seed + int64(uid)))
				client := s.makeClient(uid)
				for !s.stop.Load() {
					s.sendRequest(uid, rng, client)
				}
			}(i)
		}
	} else {
		// Count-based mode: each user sends a fixed number of requests
		for i := 0; i < s.config.NumUsers; i++ {
			wg.Add(1)
			go func(uid int) {
				defer wg.Done()
				rng := rand.New(rand.NewSource(s.config.Seed + int64(uid)))
				client := s.makeClient(uid)
				for j := 0; j < s.config.RequestsPerUser; j++ {
					s.sendRequest(uid, rng, client)
				}
			}(i)
		}
	}

	wg.Wait()
	close(done)
	log.Printf("  [SIM] done: completed=%d failed=%d", s.completed.Load(), s.failed.Load())
}

// Completed returns the number of successful requests so far.
func (s *Simulator) Completed() int64 { return s.completed.Load() }

// Failed returns the number of failed requests so far.
func (s *Simulator) Failed() int64 { return s.failed.Load() }

// Stats is a run-level summary suitable for writing summary.json.
type Stats struct {
	RunID              string           `json:"run_id"`
	ExperimentName     string           `json:"experiment_name"`
	OperationMode      string           `json:"operation_mode"`
	Completed          int64            `json:"completed"`
	Failed             int64            `json:"failed"`
	Retries            int64            `json:"retries"`
	FailureStageCounts map[string]int64 `json:"failure_stage_counts"`
	FailureCodeCounts  map[string]int64 `json:"failure_code_counts"`
	OperationCounts    map[string]int64 `json:"operation_counts"`
	CorrectnessSampled int64            `json:"correctness_sampled"`
	CorrectnessPassed  int64            `json:"correctness_passed"`
	CorrectnessFailed  int64            `json:"correctness_failed"`
}

// Stats returns a snapshot of run-level counters.
func (s *Simulator) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()

	return Stats{
		RunID:              s.config.RunID,
		ExperimentName:     s.config.ExperimentName,
		OperationMode:      normalizedOperationMode(s.config.OperationMode),
		Completed:          s.completed.Load(),
		Failed:             s.failed.Load(),
		Retries:            s.retries.Load(),
		FailureStageCounts: cloneCounterMap(s.failureStageCounts),
		FailureCodeCounts:  cloneCounterMap(s.failureCodeCounts),
		OperationCounts:    cloneCounterMap(s.operationCounts),
		CorrectnessSampled: s.correctnessSampled,
		CorrectnessPassed:  s.correctnessPassed,
		CorrectnessFailed:  s.correctnessFailed,
	}
}

func (s *Simulator) reportProgress(done chan struct{}) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	start := time.Now()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			c := s.completed.Load()
			f := s.failed.Load()
			total := c + f
			elapsed := time.Since(start).Seconds()
			rate := float64(total) / elapsed
			log.Printf("    [%s] completed=%d failed=%d rate=%.1f/s elapsed=%.1fs",
				s.config.ExperimentName, c, f, rate, elapsed)
		}
	}
}

// ---- request types (mirror gateway JSON schema) ----

type computeRequest struct {
	RequestID string   `json:"request_id"`
	Operation string   `json:"operation"`
	Payloads  []string `json:"payloads"`
}

type computeResponse struct {
	RequestID string `json:"request_id"`
	Result    string `json:"result"`
	Metrics   struct {
		GatewayProcessingMS float64 `json:"gateway_processing_ms"`
		GatewayParseMS      float64 `json:"gateway_parse_ms"`
		GatewayEnqueueMS    float64 `json:"gateway_enqueue_ms"`
		GatewayEncodeMS     float64 `json:"gateway_encode_ms"`
		PHEComputeMS        float64 `json:"phe_compute_ms"`
		QueueWaitMS         float64 `json:"queue_wait_ms"`
	} `json:"metrics"`
	Error string `json:"error"`
}

// sendRequest: generate → encrypt → send via user's proxy → record metrics.
func (s *Simulator) sendRequest(userID int, rng *rand.Rand, client *http.Client) {
	reqNum := s.reqCounter.Add(1)
	reqID := fmt.Sprintf("%s-u%d-r%d", s.config.ExperimentName, userID, reqNum)
	op := s.pickOperation(reqNum)
	s.incrementMap(s.operationCounts, op)

	e2eStart := time.Now()

	// --- Encrypt batch ---
	encStart := time.Now()
	payloads := make([]string, s.config.BatchSize)
	expected := int64(0)
	for k := 0; k < s.config.BatchSize; k++ {
		val := rng.Int63n(1000) + 1 // [1, 1000]
		expected += val
		c, err := s.pubKey.Encrypt(big.NewInt(val))
		if err != nil {
			s.recordFailure(reqID, userID, e2eStart, op, 1, "encrypt", "encrypt_error", 0, err.Error())
			return
		}
		payloads[k] = c.Text(16)
	}
	if op == "count" {
		expected = int64(s.config.BatchSize)
	}
	encDurationMS := float64(time.Since(encStart).Nanoseconds()) / 1e6

	// --- Marshal ---
	marshalStart := time.Now()
	body, err := json.Marshal(computeRequest{
		RequestID: reqID,
		Operation: op,
		Payloads:  payloads,
	})
	marshalDurationMS := float64(time.Since(marshalStart).Nanoseconds()) / 1e6
	if err != nil {
		s.recordFailure(reqID, userID, e2eStart, op, 1, "marshal", "marshal_error", 0, err.Error())
		return
	}
	ciphertextSize := len(body)
	plaintextSize := s.config.BatchSize * 8 // each int64 = 8 bytes

	maxAttempts := s.config.RetryMaxAttempts
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	var (
		cr                  computeResponse
		respStatus          int
		httpRoundtripMS     float64
		unmarshalDurationMS float64
		attempt             int
		lastStage           string
		lastCode            string
		lastErr             string
	)

	for attempt = 1; attempt <= maxAttempts; attempt++ {
		httpStart := time.Now()
		resp, reqErr := client.Post(s.config.ProxyURL+"/compute", "application/json", bytes.NewReader(body))
		httpRoundtripMS = float64(time.Since(httpStart).Nanoseconds()) / 1e6
		if reqErr != nil {
			lastStage = "http_connect"
			lastCode = classifyHTTPError(reqErr)
			lastErr = reqErr.Error()
			if attempt < maxAttempts {
				s.retries.Add(1)
				sleepRetryBackoff(s.config.RetryBackoffMS, attempt)
				continue
			}
			break
		}

		respStatus = resp.StatusCode
		respBody, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			lastStage = "http_read_body"
			lastCode = "read_body_error"
			lastErr = readErr.Error()
			if attempt < maxAttempts {
				s.retries.Add(1)
				sleepRetryBackoff(s.config.RetryBackoffMS, attempt)
				continue
			}
			break
		}

		if respStatus != http.StatusOK {
			lastStage = "http_status"
			lastCode = fmt.Sprintf("status_%d", respStatus)
			lastErr = "non-200 response"
			if attempt < maxAttempts {
				s.retries.Add(1)
				sleepRetryBackoff(s.config.RetryBackoffMS, attempt)
				continue
			}
			break
		}

		unmarshalStart := time.Now()
		if err := json.Unmarshal(respBody, &cr); err != nil {
			unmarshalDurationMS = float64(time.Since(unmarshalStart).Nanoseconds()) / 1e6
			lastStage = "unmarshal"
			lastCode = "unmarshal_error"
			lastErr = err.Error()
			if attempt < maxAttempts {
				s.retries.Add(1)
				sleepRetryBackoff(s.config.RetryBackoffMS, attempt)
				continue
			}
			break
		}
		unmarshalDurationMS = float64(time.Since(unmarshalStart).Nanoseconds()) / 1e6

		if cr.Error != "" {
			lastStage = "gateway_error"
			lastCode = "gateway_error"
			lastErr = cr.Error
			if attempt < maxAttempts {
				s.retries.Add(1)
				sleepRetryBackoff(s.config.RetryBackoffMS, attempt)
				continue
			}
			break
		}

		lastStage = ""
		lastCode = ""
		lastErr = ""
		break
	}

	e2eDurationMS := float64(time.Since(e2eStart).Nanoseconds()) / 1e6
	if lastStage != "" {
		s.recordFailure(reqID, userID, e2eStart, op, attempt, lastStage, lastCode, respStatus, lastErr)
		return
	}

	correctnessSampled := shouldSample(rng, s.config.CorrectnessSampleRate)
	expectedResult := ""
	decryptedResult := ""
	correctnessOK := false
	if correctnessSampled {
		expectedResult = fmt.Sprintf("%d", expected)
		cipher := new(big.Int)
		if _, ok := cipher.SetString(cr.Result, 16); ok {
			if m, derr := s.privKey.Decrypt(cipher); derr == nil {
				decryptedResult = m.Text(10)
				correctnessOK = m.Cmp(big.NewInt(expected)) == 0
			}
		}
		s.recordCorrectness(correctnessOK)
	}

	s.completed.Add(1)
	s.recorder.Write(metrics.Record{
		RunID:               s.config.RunID,
		ExperimentName:      s.config.ExperimentName,
		Operation:           op,
		RequestID:           reqID,
		Timestamp:           time.Now().Format(time.RFC3339Nano),
		UserID:              userID,
		BatchSize:           s.config.BatchSize,
		PlaintextSizeBytes:  plaintextSize,
		CiphertextSizeBytes: ciphertextSize,
		EncryptionTimeMS:    encDurationMS,
		MarshalTimeMS:       marshalDurationMS,
		HTTPRoundtripMS:     httpRoundtripMS,
		ResponseUnmarshalMS: unmarshalDurationMS,
		ProxyDelayMS:        s.config.ProxyDelayMS,
		GatewayProcessingMS: cr.Metrics.GatewayProcessingMS,
		GatewayParseMS:      cr.Metrics.GatewayParseMS,
		GatewayEnqueueMS:    cr.Metrics.GatewayEnqueueMS,
		GatewayEncodeMS:     cr.Metrics.GatewayEncodeMS,
		PHEComputeMS:        cr.Metrics.PHEComputeMS,
		QueueWaitMS:         cr.Metrics.QueueWaitMS,
		EndToEndLatencyMS:   e2eDurationMS,
		HTTPStatusCode:      respStatus,
		AttemptCount:        attempt,
		Retried:             attempt > 1,
		Success:             true,
		CorrectnessSampled:  correctnessSampled,
		ExpectedPlainResult: expectedResult,
		DecryptedResult:     decryptedResult,
		CorrectnessOK:       correctnessOK,
	})
}

func (s *Simulator) recordFailure(reqID string, userID int, startTime time.Time, op string, attempts int, stage, code string, httpStatus int, errMsg string) {
	s.failed.Add(1)
	s.incrementMap(s.failureStageCounts, stage)
	s.incrementMap(s.failureCodeCounts, code)
	s.recorder.Write(metrics.Record{
		RunID:             s.config.RunID,
		ExperimentName:    s.config.ExperimentName,
		Operation:         op,
		RequestID:         reqID,
		Timestamp:         time.Now().Format(time.RFC3339Nano),
		UserID:            userID,
		BatchSize:         s.config.BatchSize,
		EndToEndLatencyMS: float64(time.Since(startTime).Nanoseconds()) / 1e6,
		HTTPStatusCode:    httpStatus,
		AttemptCount:      attempts,
		Retried:           attempts > 1,
		Success:           false,
		FailureStage:      stage,
		FailureCode:       code,
		ErrorMessage:      errMsg,
	})
}

func classifyHTTPError(err error) string {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "timeout"):
		return "timeout"
	case strings.Contains(msg, "connection refused"):
		return "connection_refused"
	case strings.Contains(msg, "no such host"):
		return "dns_error"
	case strings.Contains(msg, "proxyconnect"):
		return "proxy_connect_error"
	default:
		return "http_error"
	}
}

func (s *Simulator) pickOperation(reqNum int64) string {
	mode := normalizedOperationMode(s.config.OperationMode)
	if mode == "mixed" {
		if reqNum%2 == 0 {
			return "count"
		}
		return "sum"
	}
	return mode
}

func normalizedOperationMode(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "count":
		return "count"
	case "mixed":
		return "mixed"
	default:
		return "sum"
	}
}

func shouldSample(rng *rand.Rand, rate float64) bool {
	if rate <= 0 {
		return false
	}
	if rate >= 1 {
		return true
	}
	return rng.Float64() < rate
}

func sleepRetryBackoff(backoffMS, attempt int) {
	if backoffMS <= 0 {
		return
	}
	time.Sleep(time.Duration(backoffMS*attempt) * time.Millisecond)
}

func cloneCounterMap(in map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (s *Simulator) incrementMap(m map[string]int64, key string) {
	if key == "" {
		return
	}
	s.mu.Lock()
	m[key]++
	s.mu.Unlock()
}

func (s *Simulator) recordCorrectness(ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.correctnessSampled++
	if ok {
		s.correctnessPassed++
	} else {
		s.correctnessFailed++
	}
}

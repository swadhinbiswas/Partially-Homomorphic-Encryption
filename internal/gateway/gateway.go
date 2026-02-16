package gateway

import (
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"time"

	"homomorphic/internal/crypto/paillier"
	"homomorphic/internal/worker"
)

// Gateway is a privacy-first REST API gateway that never accesses plaintext.
// It receives encrypted payloads and routes PHE operations to a worker pool.
type Gateway struct {
	Port       int
	workerPool *worker.Pool
	pubKey     *paillier.PublicKey
	server     *http.Server
}

// ComputeRequest is the JSON body sent by clients.
type ComputeRequest struct {
	RequestID string   `json:"request_id"`
	Operation string   `json:"operation"` // "sum" or "count"
	Payloads  []string `json:"payloads"`  // hex-encoded ciphertexts
}

// ComputeResponse is the JSON body returned to clients.
type ComputeResponse struct {
	RequestID string          `json:"request_id"`
	Result    string          `json:"result"` // hex-encoded ciphertext
	Metrics   ResponseMetrics `json:"metrics"`
	Error     string          `json:"error,omitempty"`
}

// ResponseMetrics are timing measurements from the gateway and worker pool.
type ResponseMetrics struct {
	GatewayProcessingMS float64 `json:"gateway_processing_ms"`
	GatewayParseMS      float64 `json:"gateway_parse_ms"`
	GatewayEnqueueMS    float64 `json:"gateway_enqueue_ms"`
	GatewayEncodeMS     float64 `json:"gateway_encode_ms"`
	PHEComputeMS        float64 `json:"phe_compute_ms"`
	QueueWaitMS         float64 `json:"queue_wait_ms"`
}

// NewGateway creates a gateway backed by a PHE worker pool.
func NewGateway(port, workers, queueSize int, pubKey *paillier.PublicKey) *Gateway {
	pool := worker.NewPool(workers, queueSize)
	pool.Start()
	return &Gateway{
		Port:       port,
		workerPool: pool,
		pubKey:     pubKey,
	}
}

// Start begins listening on the configured port. Uses its own ServeMux
// (not http.DefaultServeMux) to avoid handler conflicts when proxy runs
// in the same process.
func (g *Gateway) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/compute", g.handleCompute)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	g.server = &http.Server{
		Addr:         fmt.Sprintf(":%d", g.Port),
		Handler:      mux,
		ReadTimeout:  120 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  240 * time.Second,
	}
	return g.server.ListenAndServe()
}

func (g *Gateway) handleCompute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	start := time.Now()
	parseStart := time.Now()

	var req ComputeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "", "invalid request body: "+err.Error(), start)
		return
	}

	// Parse hex-encoded ciphertexts
	ciphertexts := make([]*big.Int, len(req.Payloads))
	for i, p := range req.Payloads {
		n := new(big.Int)
		if _, ok := n.SetString(p, 16); !ok {
			writeError(w, req.RequestID, fmt.Sprintf("invalid hex at index %d", i), start)
			return
		}
		ciphertexts[i] = n
	}

	// Map operation
	var op worker.OperationType
	switch req.Operation {
	case "sum":
		op = worker.OpSum
	case "count":
		op = worker.OpCount
	default:
		writeError(w, req.RequestID, "unsupported operation: "+req.Operation, start)
		return
	}
	parseDuration := time.Since(parseStart)

	// Submit to worker pool
	respCh := make(chan worker.Result, 1)
	job := worker.Job{
		ID:           req.RequestID,
		Op:           op,
		Payloads:     ciphertexts,
		PublicKey:    g.pubKey,
		EnqueuedAt:   time.Now(),
		ResponseChan: respCh,
	}

	enqueueStart := time.Now()
	g.workerPool.JobQueue <- job
	enqueueDuration := time.Since(enqueueStart)
	result := <-respCh

	gwDuration := time.Since(start)

	resp := ComputeResponse{
		RequestID: req.RequestID,
		Metrics: ResponseMetrics{
			GatewayProcessingMS: nsToMS(gwDuration),
			GatewayParseMS:      nsToMS(parseDuration),
			GatewayEnqueueMS:    nsToMS(enqueueDuration),
			PHEComputeMS:        nsToMS(result.ComputeDuration),
			QueueWaitMS:         nsToMS(result.QueueDuration),
		},
	}

	if result.Error != nil {
		resp.Error = result.Error.Error()
	} else if result.EncryptedResult != nil {
		resp.Result = result.EncryptedResult.Text(16)
	}

	encodeStart := time.Now()
	payload, err := json.Marshal(resp)
	if err != nil {
		writeError(w, req.RequestID, "response encode error: "+err.Error(), start)
		return
	}
	resp.Metrics.GatewayEncodeMS = nsToMS(time.Since(encodeStart))

	payload, err = json.Marshal(resp)
	if err != nil {
		writeError(w, req.RequestID, "response encode error: "+err.Error(), start)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(payload)
}

func writeError(w http.ResponseWriter, reqID, msg string, start time.Time) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	json.NewEncoder(w).Encode(ComputeResponse{
		RequestID: reqID,
		Error:     msg,
		Metrics:   ResponseMetrics{GatewayProcessingMS: nsToMS(time.Since(start))},
	})
}

func nsToMS(d time.Duration) float64 {
	return float64(d.Nanoseconds()) / 1e6
}

package metrics

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
)

// Record holds all timing and size data for a single request.
// Every field is raw measurement — no derived or computed values.
type Record struct {
	RunID               string  `json:"run_id"`
	ExperimentName      string  `json:"experiment_name"`
	Operation           string  `json:"operation"`
	RequestID           string  `json:"request_id"`
	Timestamp           string  `json:"timestamp"`
	UserID              int     `json:"user_id"`
	BatchSize           int     `json:"batch_size"`
	PlaintextSizeBytes  int     `json:"plaintext_size_bytes"`
	CiphertextSizeBytes int     `json:"ciphertext_size_bytes"`
	EncryptionTimeMS    float64 `json:"encryption_time_ms"`
	MarshalTimeMS       float64 `json:"marshal_time_ms"`
	HTTPRoundtripMS     float64 `json:"http_roundtrip_ms"`
	ResponseUnmarshalMS float64 `json:"response_unmarshal_ms"`
	ProxyDelayMS        float64 `json:"proxy_delay_ms"`
	GatewayProcessingMS float64 `json:"gateway_processing_ms"`
	GatewayParseMS      float64 `json:"gateway_parse_ms"`
	GatewayEnqueueMS    float64 `json:"gateway_enqueue_ms"`
	GatewayEncodeMS     float64 `json:"gateway_encode_ms"`
	PHEComputeMS        float64 `json:"phe_compute_ms"`
	QueueWaitMS         float64 `json:"queue_wait_ms"`
	EndToEndLatencyMS   float64 `json:"end_to_end_latency_ms"`
	HTTPStatusCode      int     `json:"http_status_code"`
	AttemptCount        int     `json:"attempt_count"`
	Retried             bool    `json:"retried"`
	Success             bool    `json:"success"`
	FailureStage        string  `json:"failure_stage,omitempty"`
	FailureCode         string  `json:"failure_code,omitempty"`
	CorrectnessSampled  bool    `json:"correctness_sampled"`
	ExpectedPlainResult string  `json:"expected_plain_result,omitempty"`
	DecryptedResult     string  `json:"decrypted_result,omitempty"`
	CorrectnessOK       bool    `json:"correctness_ok"`
	ErrorMessage        string  `json:"error_message,omitempty"`
}

var csvHeader = []string{
	"run_id", "experiment_name", "operation", "request_id",
	"timestamp", "user_id", "batch_size",
	"plaintext_size_bytes", "ciphertext_size_bytes",
	"encryption_time_ms", "marshal_time_ms", "http_roundtrip_ms", "response_unmarshal_ms",
	"proxy_delay_ms", "gateway_processing_ms", "gateway_parse_ms", "gateway_enqueue_ms", "gateway_encode_ms",
	"phe_compute_ms", "queue_wait_ms", "end_to_end_latency_ms",
	"http_status_code", "attempt_count", "retried",
	"success", "failure_stage", "failure_code",
	"correctness_sampled", "expected_plain_result", "decrypted_result", "correctness_ok",
	"error_message",
}

// StreamRecorder writes records to chunked CSV and JSONL files on disk.
// It never holds more than one channel-buffer of records in memory.
// Safe for concurrent use from multiple goroutines.
type StreamRecorder struct {
	baseDir   string
	chunkSize int64

	recordCh chan Record
	wg       sync.WaitGroup
	total    atomic.Int64

	// internal — only touched by the drain goroutine
	chunkNum int
	count    int64
	csvFile  *os.File
	csvW     *csv.Writer
	jsonFile *os.File
}

// NewStreamRecorder creates a recorder that writes chunked CSV+JSONL
// into baseDir. Each chunk holds at most chunkSize records.
func NewStreamRecorder(baseDir string, chunkSize int64) (*StreamRecorder, error) {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", baseDir, err)
	}

	sr := &StreamRecorder{
		baseDir:   baseDir,
		chunkSize: chunkSize,
		recordCh:  make(chan Record, 50000),
	}

	if err := sr.openChunk(); err != nil {
		return nil, err
	}

	sr.wg.Add(1)
	go sr.drain()

	return sr, nil
}

func (sr *StreamRecorder) openChunk() error {
	csvPath := filepath.Join(sr.baseDir, fmt.Sprintf("chunk_%04d.csv", sr.chunkNum))
	jsonPath := filepath.Join(sr.baseDir, fmt.Sprintf("chunk_%04d.jsonl", sr.chunkNum))

	var err error
	sr.csvFile, err = os.Create(csvPath)
	if err != nil {
		return fmt.Errorf("create csv %s: %w", csvPath, err)
	}
	sr.csvW = csv.NewWriter(sr.csvFile)
	if err := sr.csvW.Write(csvHeader); err != nil {
		return fmt.Errorf("write csv header: %w", err)
	}

	sr.jsonFile, err = os.Create(jsonPath)
	if err != nil {
		return fmt.Errorf("create jsonl %s: %w", jsonPath, err)
	}

	sr.count = 0
	return nil
}

func (sr *StreamRecorder) rotateChunk() error {
	sr.csvW.Flush()
	sr.csvFile.Close()
	sr.jsonFile.Close()

	sr.chunkNum++
	return sr.openChunk()
}

// drain is the single goroutine that reads from recordCh and writes to disk.
func (sr *StreamRecorder) drain() {
	defer sr.wg.Done()

	jsonEnc := json.NewEncoder(sr.jsonFile)

	for rec := range sr.recordCh {
		row := []string{
			rec.RunID,
			rec.ExperimentName,
			rec.Operation,
			rec.RequestID,
			rec.Timestamp,
			strconv.Itoa(rec.UserID),
			strconv.Itoa(rec.BatchSize),
			strconv.Itoa(rec.PlaintextSizeBytes),
			strconv.Itoa(rec.CiphertextSizeBytes),
			strconv.FormatFloat(rec.EncryptionTimeMS, 'f', 4, 64),
			strconv.FormatFloat(rec.MarshalTimeMS, 'f', 4, 64),
			strconv.FormatFloat(rec.HTTPRoundtripMS, 'f', 4, 64),
			strconv.FormatFloat(rec.ResponseUnmarshalMS, 'f', 4, 64),
			strconv.FormatFloat(rec.ProxyDelayMS, 'f', 4, 64),
			strconv.FormatFloat(rec.GatewayProcessingMS, 'f', 4, 64),
			strconv.FormatFloat(rec.GatewayParseMS, 'f', 4, 64),
			strconv.FormatFloat(rec.GatewayEnqueueMS, 'f', 4, 64),
			strconv.FormatFloat(rec.GatewayEncodeMS, 'f', 4, 64),
			strconv.FormatFloat(rec.PHEComputeMS, 'f', 4, 64),
			strconv.FormatFloat(rec.QueueWaitMS, 'f', 4, 64),
			strconv.FormatFloat(rec.EndToEndLatencyMS, 'f', 4, 64),
			strconv.Itoa(rec.HTTPStatusCode),
			strconv.Itoa(rec.AttemptCount),
			strconv.FormatBool(rec.Retried),
			strconv.FormatBool(rec.Success),
			rec.FailureStage,
			rec.FailureCode,
			strconv.FormatBool(rec.CorrectnessSampled),
			rec.ExpectedPlainResult,
			rec.DecryptedResult,
			strconv.FormatBool(rec.CorrectnessOK),
			rec.ErrorMessage,
		}
		_ = sr.csvW.Write(row)
		_ = jsonEnc.Encode(rec)

		sr.count++
		sr.total.Add(1)

		// Rotate chunk when limit reached
		if sr.count >= sr.chunkSize {
			if err := sr.rotateChunk(); err != nil {
				log.Printf("[RECORDER] ERROR rotating chunk: %v", err)
				return
			}
			jsonEnc = json.NewEncoder(sr.jsonFile)
		} else if sr.count%5000 == 0 {
			// Periodic flush for durability
			sr.csvW.Flush()
		}
	}

	// Final flush
	sr.csvW.Flush()
	sr.csvFile.Close()
	sr.jsonFile.Close()
}

// Write sends a record to the background writer. Blocks if the internal
// buffer (50 000 records) is full, providing natural backpressure.
func (sr *StreamRecorder) Write(rec Record) {
	sr.recordCh <- rec
}

// Close flushes all buffered records and closes files.
func (sr *StreamRecorder) Close() {
	close(sr.recordCh)
	sr.wg.Wait()
}

// Total returns the number of records written so far (atomic, safe to call concurrently).
func (sr *StreamRecorder) Total() int64 {
	return sr.total.Load()
}

package worker

import (
	"math/big"
	"time"

	"homomorphic/internal/crypto/paillier"
)

// OperationType defines the PHE operation to perform.
type OperationType int

const (
	OpSum   OperationType = iota // Homomorphic addition of all ciphertexts
	OpCount                      // Encrypted count of items
)

// Job is a unit of work submitted to the pool.
type Job struct {
	ID           string
	Op           OperationType
	Payloads     []*big.Int // Ciphertexts
	PublicKey    *paillier.PublicKey
	EnqueuedAt   time.Time
	ResponseChan chan Result
}

// Result is the outcome of a job.
type Result struct {
	JobID           string
	EncryptedResult *big.Int
	ComputeDuration time.Duration
	QueueDuration   time.Duration
	Error           error
}

// Pool manages a fixed set of worker goroutines reading from a shared job queue.
type Pool struct {
	JobQueue   chan Job
	NumWorkers int
	quit       chan struct{}
}

// NewPool creates a worker pool. queueSize controls the buffered channel depth.
func NewPool(numWorkers, queueSize int) *Pool {
	return &Pool{
		JobQueue:   make(chan Job, queueSize),
		NumWorkers: numWorkers,
		quit:       make(chan struct{}),
	}
}

// Start launches all worker goroutines.
func (p *Pool) Start() {
	for i := 0; i < p.NumWorkers; i++ {
		go p.worker()
	}
}

// Stop signals all workers to exit.
func (p *Pool) Stop() {
	close(p.quit)
}

func (p *Pool) worker() {
	for {
		select {
		case <-p.quit:
			return
		case job, ok := <-p.JobQueue:
			if !ok {
				return
			}

			startCompute := time.Now()
			queueDuration := startCompute.Sub(job.EnqueuedAt)

			var res *big.Int
			var err error

			switch job.Op {
			case OpSum:
				res = computeSum(job.PublicKey, job.Payloads)
			case OpCount:
				res, err = computeCount(job.PublicKey, job.Payloads)
			}

			computeDuration := time.Since(startCompute)

			if job.ResponseChan != nil {
				job.ResponseChan <- Result{
					JobID:           job.ID,
					EncryptedResult: res,
					ComputeDuration: computeDuration,
					QueueDuration:   queueDuration,
					Error:           err,
				}
			}
		}
	}
}

// computeSum performs homomorphic addition: E(m1+m2+...+mn) = E(m1)*E(m2)*...*E(mn) mod N^2
func computeSum(pub *paillier.PublicKey, ciphertexts []*big.Int) *big.Int {
	if len(ciphertexts) == 0 {
		return big.NewInt(0)
	}
	acc := new(big.Int).Set(ciphertexts[0])
	for i := 1; i < len(ciphertexts); i++ {
		acc = pub.Add(acc, ciphertexts[i])
	}
	return acc
}

// computeCount returns E(len(ciphertexts)) using the public key.
// Encrypts 1 and multiplies by the count: E(1)^n = E(n).
func computeCount(pub *paillier.PublicKey, ciphertexts []*big.Int) (*big.Int, error) {
	enc1, err := pub.Encrypt(big.NewInt(1))
	if err != nil {
		return nil, err
	}
	n := big.NewInt(int64(len(ciphertexts)))
	return pub.MulPlain(enc1, n), nil
}

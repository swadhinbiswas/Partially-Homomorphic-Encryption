package proxy

import (
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"time"
)

// Config holds all tunable proxy parameters.
type Config struct {
	TargetURL       string
	Port            int
	LatencyFixed    time.Duration
	LatencyJitter   time.Duration
	DropProbability float64 // 0.0–1.0
}

// Proxy is an L7 reverse proxy that simulates real deployment conditions.
// It never decrypts or inspects ciphertext (honest-but-curious model).
type Proxy struct {
	mu       sync.RWMutex
	config   Config
	revProxy *httputil.ReverseProxy
	server   *http.Server
}

// New creates a proxy pointing at the given target URL.
func New(cfg Config) (*Proxy, error) {
	target, err := url.Parse(cfg.TargetURL)
	if err != nil {
		return nil, fmt.Errorf("parse target url: %w", err)
	}

	rp := httputil.NewSingleHostReverseProxy(target)
	rp.Transport = &http.Transport{
		MaxIdleConns:        2000,
		MaxIdleConnsPerHost: 2000,
		IdleConnTimeout:     120 * time.Second,
	}

	return &Proxy{
		config:   cfg,
		revProxy: rp,
	}, nil
}

// Start begins listening. Blocks until the server stops.
func (p *Proxy) Start() error {
	p.server = &http.Server{
		Addr:         fmt.Sprintf(":%d", p.config.Port),
		Handler:      http.HandlerFunc(p.handle),
		ReadTimeout:  120 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  240 * time.Second,
	}
	return p.server.ListenAndServe()
}

func (p *Proxy) handle(w http.ResponseWriter, r *http.Request) {
	p.mu.RLock()
	cfg := p.config
	p.mu.RUnlock()

	// --- Metadata observation (honest-but-curious: headers, sizes, timing only) ---
	_ = r.ContentLength
	_ = r.Header

	// --- Fault injection: random drop ---
	if cfg.DropProbability > 0 && rand.Float64() < cfg.DropProbability {
		http.Error(w, "proxy: connection dropped (fault injection)", http.StatusServiceUnavailable)
		return
	}

	// --- Artificial latency (fixed + jitter) ---
	delay := cfg.LatencyFixed
	if cfg.LatencyJitter > 0 {
		delay += time.Duration(rand.Int63n(int64(cfg.LatencyJitter)))
	}
	if delay > 0 {
		time.Sleep(delay)
	}

	// --- Forward transparently (no ciphertext inspection) ---
	p.revProxy.ServeHTTP(w, r)
}

// UpdateConfig atomically updates the mutable proxy parameters.
// TargetURL and Port are not changed at runtime.
func (p *Proxy) UpdateConfig(cfg Config) {
	p.mu.Lock()
	defer p.mu.Unlock()
	old := p.config
	p.config.LatencyFixed = cfg.LatencyFixed
	p.config.LatencyJitter = cfg.LatencyJitter
	p.config.DropProbability = cfg.DropProbability
	log.Printf("[PROXY] config updated: latency=%v jitter=%v drop=%.2f%% (was: latency=%v drop=%.2f%%)",
		cfg.LatencyFixed, cfg.LatencyJitter, cfg.DropProbability*100,
		old.LatencyFixed, old.DropProbability*100)
}

// GetConfig returns a snapshot of the current configuration.
func (p *Proxy) GetConfig() Config {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.config
}

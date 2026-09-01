package server

import (
	"net/http"
	"strings"
	"sync"
)

// Readiness is a small registry of named checks; the service is ready when
// every check passes. Future dependencies register here rather than growing
// the handler.
type readyCheck struct {
	name string
	fn   func() error
}

type readiness struct {
	mu     sync.RWMutex
	checks []readyCheck
}

func (r *readiness) register(name string, fn func() error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checks = append(r.checks, readyCheck{name: name, fn: fn})
}

// failed returns the names of failing checks (empty = ready).
func (r *readiness) failed() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var failed []string
	for _, c := range r.checks {
		if err := c.fn(); err != nil {
			failed = append(failed, c.name)
		}
	}
	return failed
}

// RegisterReadyCheck adds a named readiness dependency. Exported so embedding
// deployments can attach their own checks.
func (s *Server) RegisterReadyCheck(name string, fn func() error) {
	s.ready.register(name, fn)
}

// handleLiveness is deliberately dependency-free: the process is up and the
// HTTP stack answers.
func (s *Server) handleLiveness(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func (s *Server) handleReadiness(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	failed := s.ready.failed()
	if len(failed) == 0 {
		_, _ = w.Write([]byte(`{"status":"ok"}`))
		return
	}
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(`{"status":"unavailable","failed":["` + strings.Join(failed, `","`) + `"]}`))
}

// handleHealthLegacy keeps the historical /health contract (plain "ok", 200)
// while mirroring readiness for orchestrators that still point at it.
func (s *Server) handleHealthLegacy(w http.ResponseWriter, r *http.Request) {
	if failed := s.ready.failed(); len(failed) > 0 {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	_, _ = w.Write([]byte("ok"))
}

// isProbePath reports whether the request targets a probe endpoint —
// exempted from auth and demoted to debug in access logs.
func (s *Server) isProbePath(path string) bool {
	return path == s.cfg.LivenessPath || path == s.cfg.ReadinessPath || path == "/health"
}

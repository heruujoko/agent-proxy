package server

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"
)

// Health exposes dependency-independent liveness and bounded readiness checks.
// A Health must not be copied after first use.
type Health struct {
	check    func(context.Context) error
	timeout  time.Duration
	stopping atomic.Bool
}

// NewHealth creates health handlers with a per-request readiness deadline.
// check must honor context cancellation and bound its entire operation,
// including connection setup, pool waiting, and retries, by that deadline.
func NewHealth(check func(context.Context) error, timeout time.Duration) *Health {
	return &Health{check: check, timeout: timeout}
}

// Handler serves only GET /healthz and GET /readyz.
func (h *Health) Handler() http.Handler {
	return http.HandlerFunc(h.serveHTTP)
}

// Stop permanently marks the service unready before HTTP draining begins.
func (h *Health) Stop() {
	h.stopping.Store(true)
}

func (h *Health) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/healthz" && r.URL.Path != "/readyz" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path == "/healthz" {
		w.WriteHeader(http.StatusOK)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), h.timeout)
	defer cancel()
	if h.stopping.Load() || ctx.Err() != nil {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
		return
	}
	if err := h.check(ctx); err != nil || ctx.Err() != nil || h.stopping.Load() {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
}

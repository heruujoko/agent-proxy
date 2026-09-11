package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestHealthRemainsLiveWhenDependencyFails(t *testing.T) {
	h := NewHealth(func(context.Context) error {
		return errors.New("secret-sentinel")
	}, time.Second)

	for _, tc := range []struct {
		path string
		want int
	}{
		{"/healthz", http.StatusOK},
		{"/readyz", http.StatusServiceUnavailable},
	} {
		t.Run(tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d", w.Code, tc.want)
			}
			if strings.Contains(w.Body.String(), "secret-sentinel") {
				t.Fatal("health response disclosed an upstream error")
			}
		})
	}
}

func TestHealthReadinessRecoversWithoutRestart(t *testing.T) {
	available := false
	h := NewHealth(func(context.Context) error {
		if !available {
			return errors.New("unavailable")
		}
		return nil
	}, time.Second)

	for _, state := range []bool{false, true, false, true} {
		available = state
		w := httptest.NewRecorder()
		h.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		want := http.StatusServiceUnavailable
		if state {
			want = http.StatusOK
		}
		if w.Code != want {
			t.Fatalf("available = %t: status = %d, want %d", state, w.Code, want)
		}
	}
}

func TestHealthRoutesAreExactAndGetOnly(t *testing.T) {
	handler := NewHealth(func(context.Context) error { return nil }, time.Second).Handler()
	for _, tc := range []struct {
		method string
		path   string
		want   int
	}{
		{http.MethodGet, "/", http.StatusNotFound},
		{http.MethodGet, "/missing", http.StatusNotFound},
		{http.MethodGet, "/healthz/", http.StatusNotFound},
		{http.MethodGet, "/readyz/child", http.StatusNotFound},
		{http.MethodPost, "/healthz", http.StatusMethodNotAllowed},
		{http.MethodHead, "/readyz", http.StatusMethodNotAllowed},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d", w.Code, tc.want)
			}
			if tc.want == http.StatusMethodNotAllowed && w.Header().Get("Allow") != http.MethodGet {
				t.Fatalf("Allow = %q, want GET", w.Header().Get("Allow"))
			}
		})
	}
}

func TestHealthReadinessDeadlineRejectsLateSuccess(t *testing.T) {
	h := NewHealth(func(ctx context.Context) error {
		<-ctx.Done()
		return nil
	}, 20*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	w := httptest.NewRecorder()
	started := time.Now()
	h.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil).WithContext(ctx))
	elapsed := time.Since(started)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d after deadline, want 503", w.Code)
	}
	if elapsed >= time.Second {
		t.Fatalf("readiness exceeded its 20ms budget with scheduling allowance: %s", elapsed)
	}
}

func TestHealthReadinessRejectsAlreadyCancelledRequest(t *testing.T) {
	h := NewHealth(func(context.Context) error { return nil }, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := httptest.NewRecorder()
	h.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil).WithContext(ctx))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d for cancelled request, want 503", w.Code)
	}
}

func TestHealthCancellationDuringCheckRejectsSuccess(t *testing.T) {
	entered := make(chan struct{})
	h := NewHealth(func(ctx context.Context) error {
		close(entered)
		<-ctx.Done()
		return nil
	}, 10*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		h.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil).WithContext(ctx))
		result <- w
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("readiness check did not start")
	}
	cancel()
	w := awaitHealthResponse(t, result)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d after cancellation, want 503", w.Code)
	}
}

func TestHealthStopRejectsInflightSuccessAndKeepsLiveness(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	h := NewHealth(func(ctx context.Context) error {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}, 10*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		h.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil).WithContext(ctx))
		result <- w
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("readiness check did not start")
	}

	// Liveness must finish even while the dependency check is blocked.
	live := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		h.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil).WithContext(ctx))
		live <- w
	}()
	if w := awaitHealthResponse(t, live); w.Code != http.StatusOK {
		t.Fatalf("liveness status = %d during dependency check, want 200", w.Code)
	}

	h.Stop()
	close(release)
	if w := awaitHealthResponse(t, result); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("in-flight status = %d after stopping, want 503", w.Code)
	}
	for _, tc := range []struct {
		path string
		want int
	}{
		{"/healthz", http.StatusOK},
		{"/readyz", http.StatusServiceUnavailable},
	} {
		w := httptest.NewRecorder()
		h.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if w.Code != tc.want {
			t.Fatalf("%s after stopping: status = %d, want %d", tc.path, w.Code, tc.want)
		}
	}
}

func TestHealthConcurrentStopAndReadiness(t *testing.T) {
	h := NewHealth(func(context.Context) error { return nil }, time.Second)
	handler := h.Handler()
	start := make(chan struct{})
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() {
			<-start
			for range 16 {
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
				if w.Code != http.StatusOK && w.Code != http.StatusServiceUnavailable {
					t.Errorf("concurrent readiness status = %d, want 200 or 503", w.Code)
				}
			}
		})
	}
	workers.Go(func() {
		<-start
		h.Stop()
	})
	close(start)
	workers.Wait()

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d after concurrent stopping, want 503", w.Code)
	}
}

func awaitHealthResponse(t *testing.T, result <-chan *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	t.Helper()
	select {
	case w := <-result:
		return w
	case <-time.After(2 * time.Second):
		t.Fatal("health handler did not finish within the test deadline")
		return nil
	}
}

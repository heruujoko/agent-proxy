package server

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestServeDrainsActiveRequest(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	entered := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	allow := make(chan struct{})
	h := NewHealth(func(context.Context) error { return nil }, time.Second)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-allow:
		case <-release:
		}
		_, _ = io.WriteString(w, "completed")
	})}
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, srv, listener, h, time.Second) }()
	client := &http.Client{Timeout: 2 * time.Second}
	response := make(chan error, 1)
	go func() {
		r, err := client.Get("http://" + listener.Addr().String())
		if err == nil {
			defer r.Body.Close()
			var b []byte
			b, err = io.ReadAll(r.Body)
			if err == nil && (r.StatusCode != http.StatusOK || string(b) != "completed") {
				err = io.ErrUnexpectedEOF
			}
		}
		response <- err
	}()
	waitSignal(t, entered)
	cancel()
	waitUnready(t, h)
	select {
	case err := <-done:
		t.Fatalf("returned before active request drained: %v", err)
	default:
	}
	close(allow)
	if err := waitResult(t, response); err != nil {
		t.Fatalf("active response was not delivered: %v", err)
	}
	if err := waitResult(t, done); err != nil {
		t.Fatalf("graceful shutdown failed: %v", err)
	}
}

func TestServeForcesClosureAfterDrainDeadline(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	entered := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	h := NewHealth(func(context.Context) error { return nil }, time.Second)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
	})}
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, srv, listener, h, 30*time.Millisecond) }()
	response := make(chan error, 1)
	go func() {
		client := &http.Client{Timeout: 2 * time.Second}
		r, err := client.Get("http://" + listener.Addr().String())
		if r != nil {
			r.Body.Close()
		}
		response <- err
	}()
	waitSignal(t, entered)
	cancel()
	if err := waitResult(t, done); err == nil {
		t.Fatal("drain timeout reported success")
	}
	if err := waitResult(t, response); err == nil {
		t.Fatal("blocked HTTP request survived force-close")
	}
}

func TestServeReportsUnexpectedListenerFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener.Close()
	h := NewHealth(func(context.Context) error { return nil }, time.Second)
	if err := Serve(context.Background(), &http.Server{Handler: h.Handler()}, listener, h, time.Second); err == nil {
		t.Fatal("closed listener reported successful serving")
	}
}

func waitSignal(t *testing.T, c <-chan struct{}) {
	t.Helper()
	select {
	case <-c:
	case <-time.After(2 * time.Second):
		t.Fatal("request did not enter handler")
	}
}

func waitResult(t *testing.T, c <-chan error) error {
	t.Helper()
	select {
	case err := <-c:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("lifecycle operation did not finish")
		return nil
	}
}

func waitUnready(t *testing.T, h *Health) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		w := httptest.NewRecorder()
		h.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		if w.Code == http.StatusServiceUnavailable {
			return
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("shutdown did not withdraw readiness")
		}
	}
}

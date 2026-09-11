package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"
)

// Serve drains HTTP before returning so its caller can then close dependencies.
func Serve(ctx context.Context, srv *http.Server, listener net.Listener, health *Health, shutdownTimeout time.Duration) error {
	done := make(chan error, 1)
	go func() { done <- srv.Serve(listener) }()

	select {
	case <-ctx.Done():
		health.Stop()
		drain, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(drain); err != nil {
			_ = srv.Close()
			<-done
			return errors.New("HTTP drain failed")
		}
		if err := <-done; !errors.Is(err, http.ErrServerClosed) {
			return errors.New("HTTP serving failed")
		}
		return nil
	case <-done:
		health.Stop()
		_ = srv.Close()
		return errors.New("HTTP serving failed")
	}
}

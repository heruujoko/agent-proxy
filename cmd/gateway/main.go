package main

import (
	"context"
	"flag"
	"io"
	"log"
	"log/slog"
	"math"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/heruujoko/agent-proxy/internal/config"
	"github.com/heruujoko/agent-proxy/internal/server"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stderr io.Writer) int {
	logger := slog.New(slog.NewJSONHandler(stderr, nil))
	flags := flag.NewFlagSet("gateway", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	path := flags.String("config", "", "configuration YAML path (required)")
	if err := flags.Parse(args); err != nil || *path == "" || flags.NArg() != 0 {
		logger.Error("startup failed", "reason", "invalid_arguments")
		return 1
	}
	cfg, err := config.Load(*path)
	if err != nil {
		logger.Error("startup failed", "reason", "invalid_configuration", "detail", err.Error())
		return 1
	}
	logger = slog.New(slog.NewJSONHandler(stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	client := newRedisClient(cfg.Redis, cfg.Server.ReadinessTimeout)
	defer client.Close()
	health := server.NewHealth(func(ctx context.Context) error {
		err := pingRedis(ctx, client)
		if err != nil {
			logger.Debug("readiness failed", "reason", "redis_unavailable")
		}
		return err
	}, cfg.Server.ReadinessTimeout)
	listener, err := net.Listen("tcp", cfg.Server.Listen)
	if err != nil {
		logger.Error("startup failed", "reason", "listen_failed")
		return 1
	}
	srv := &http.Server{
		Handler:           health.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       responseTimeout(cfg.Server.ReadinessTimeout),
		WriteTimeout:      responseTimeout(cfg.Server.ReadinessTimeout),
		IdleTimeout:       60 * time.Second,
		ErrorLog:          log.New(httpDiagnostics{logger}, "", 0),
	}
	logger.Info("HTTP listener started")
	if err := server.Serve(ctx, srv, listener, health, cfg.Server.ShutdownTimeout); err != nil {
		logger.Error("service stopped", "reason", "http_lifecycle_failed")
		return 1
	}
	logger.Info("service stopped")
	return 0
}

func newRedisClient(cfg config.Redis, timeout time.Duration) *redis.Pool {
	return &redis.Pool{
		MaxIdle:   2,
		MaxActive: 8,
		Wait:      true,
		DialContext: func(ctx context.Context) (redis.Conn, error) {
			conn, err := redis.DialContext(ctx, "tcp", cfg.Address,
				redis.DialConnectTimeout(timeout),
				redis.DialReadTimeout(timeout),
				redis.DialWriteTimeout(timeout),
			)
			if err != nil {
				return nil, err
			}
			switch {
			case cfg.Username != "":
				_, err = redis.DoContext(conn, ctx, "AUTH", cfg.Username, cfg.Password)
			case cfg.Password != "":
				_, err = redis.DoContext(conn, ctx, "AUTH", cfg.Password)
			}
			if err != nil {
				_ = conn.Close()
				return nil, err
			}
			return conn, nil
		},
	}
}

func pingRedis(ctx context.Context, pool *redis.Pool) error {
	conn, err := pool.GetContext(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = redis.DoContext(conn, ctx, "PING")
	return err
}

func responseTimeout(readiness time.Duration) time.Duration {
	const margin = 5 * time.Second
	if readiness > time.Duration(math.MaxInt64)-margin {
		return time.Duration(math.MaxInt64)
	}
	return readiness + margin
}

// Upstream HTTP error text can embed header values; log only the category.
type httpDiagnostics struct{ logger *slog.Logger }

func (d httpDiagnostics) Write(p []byte) (int, error) {
	d.logger.Error("HTTP server diagnostic")
	return len(p), nil
}

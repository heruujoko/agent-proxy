package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/heruujoko/agent-proxy/internal/config"
	"github.com/heruujoko/agent-proxy/internal/server"
)

// A peer that accepts TCP but never answers exposes handshake/read deadline bugs
// that checking Redis option fields or a mocked checker would not detect.
func TestReadinessBoundsRedisWireOperations(t *testing.T) {
	for _, requestDeadline := range []bool{false, true} {
		name := "readiness_deadline"
		if requestDeadline {
			name = "earlier_request_deadline"
		}
		t.Run(name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			release := make(chan struct{})
			defer close(release)
			accepted := make(chan struct{})
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				close(accepted)
				<-release
			}()
			budget := 40 * time.Millisecond
			clientTimeout := budget
			ctx := context.Background()
			if requestDeadline {
				clientTimeout = time.Second
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, budget)
				defer cancel()
			}
			client := newRedisClient(config.Redis{Address: listener.Addr().String()}, clientTimeout)
			defer client.Close()
			health := server.NewHealth(func(ctx context.Context) error {
				return pingRedis(ctx, client)
			}, clientTimeout)
			w := httptest.NewRecorder()
			start := time.Now()
			health.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil).WithContext(ctx))
			if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
				t.Fatalf("Redis operation exceeded its 40ms budget: %s", elapsed)
			}
			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("silent Redis peer yielded status %d", w.Code)
			}
			select {
			case <-accepted:
			default:
				t.Fatal("readiness did not exercise actual Redis transport")
			}
		})
	}
}

func TestReadinessCancellationClosesRedisConnection(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	connections := make(chan net.Conn, 1)
	defer func() {
		select {
		case conn := <-connections:
			conn.Close()
		default:
		}
	}()
	started := make(chan struct{})
	closed := make(chan struct{})
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		connections <- conn
		var first [1]byte
		if _, err := conn.Read(first[:]); err != nil {
			return
		}
		close(started)
		_, _ = io.Copy(io.Discard, conn)
		close(closed)
	}()
	client := newRedisClient(config.Redis{Address: listener.Addr().String()}, 2*time.Second)
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := server.NewHealth(func(ctx context.Context) error {
		return pingRedis(ctx, client)
	}, 2*time.Second)
	done := make(chan int, 1)
	go func() {
		w := httptest.NewRecorder()
		h.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil).WithContext(ctx))
		done <- w.Code
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("Redis operation never reached the wire")
	}
	cancel()
	select {
	case code := <-done:
		if code != http.StatusServiceUnavailable {
			t.Fatalf("cancelled readiness returned %d", code)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("request cancellation did not interrupt Redis operation")
	}
	select {
	case <-closed:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("cancelled Redis operation left the socket open")
	}
}

func TestConfiguredRedisUserDoesNotFallBackToDefaultAccount(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		peer := redis.NewConn(conn, time.Second, time.Second)
		defer peer.Close()
		args, err := redis.Values(peer.Receive())
		if err != nil || len(args) == 0 {
			t.Error("missing Redis command")
			return
		}
		command, _ := redis.String(args[0], nil)
		if command == "AUTH" {
			if len(args) != 3 {
				t.Error("ACL authentication did not include username and password")
			} else {
				username, _ := redis.String(args[1], nil)
				password, _ := redis.String(args[2], nil)
				if username != "restricted" || password != "" {
					t.Error("ACL authentication used different credentials")
				}
			}
			_, _ = io.WriteString(conn, "-WRONGPASS invalid username-password pair\r\n")
		} else {
			// A default nopass account permits PING without AUTH. Using it
			// would silently ignore the explicitly configured restricted user.
			_, _ = io.WriteString(conn, "+PONG\r\n")
		}
	}()
	client := newRedisClient(config.Redis{
		Address:  listener.Addr().String(),
		Username: "restricted",
	}, time.Second)
	defer client.Close()
	h := server.NewHealth(func(ctx context.Context) error {
		return pingRedis(ctx, client)
	}, time.Second)
	w := httptest.NewRecorder()
	h.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Redis fixture did not finish")
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("configured Redis user was bypassed: readiness returned %d", w.Code)
	}
}

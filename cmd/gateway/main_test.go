package main

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunRejectsInvalidArgumentsWithoutDisclosure(t *testing.T) {
	for _, args := range [][]string{
		{}, {"--unknown-secret-sentinel"},
		{"--config", "missing-secret-sentinel.yaml"},
		{"--config", "missing.yaml", "secret-sentinel"},
	} {
		var output bytes.Buffer
		if code := run(context.Background(), args, &output); code == 0 {
			t.Fatalf("invalid startup succeeded for %q", args)
		}
		if strings.Contains(output.String(), "secret-sentinel") {
			t.Fatal("startup disclosed input")
		}
	}
}

func TestRunReportsBindFailureWithoutLeakingCredentials(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	t.Setenv("GATEWAY_TEST_PASSWORD", "secret-sentinel")
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := "server:\n  listen: " + listener.Addr().String() + "\nredis:\n  password_env: GATEWAY_TEST_PASSWORD\n"
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if code := run(context.Background(), []string{"--config", path}, &output); code == 0 {
		t.Fatal("startup succeeded with occupied listener")
	}
	if strings.Contains(output.String(), "secret-sentinel") {
		t.Fatal("bind failure disclosed credentials")
	}
}

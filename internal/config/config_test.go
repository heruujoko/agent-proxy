package config_test

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/heruujoko/agent-proxy/internal/config"
)

func configFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadRejectsInvalidInputWithoutDisclosure(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"empty document", "\n"},
		{"null root", "null\n"},
		{"sequence root", "[]\n"},
		{"scalar root", "secret-sentinel\n"},
		{"unknown root", "secret-sentinel: true\n"},
		{"unknown nested field", "server:\n  secret-sentinel: true\n"},
		{"inline password", "redis:\n  password: secret-sentinel\n"},
		{"duplicate section", "redis: {}\nredis: {}\n"},
		{"duplicate nested field", "redis:\n  address: localhost:6379\n  address: localhost:6380\n"},
		{"null section", "server: null\n"},
		{"implicit null section", "log:\n"},
		{"sequence section", "redis: []\n"},
		{"scalar section", "server: secret-sentinel\n"},
		{"null field", "redis:\n  username: null\n"},
		{"null password variable", "redis:\n  password_env: null\n"},
		{"null listen", "server:\n  listen: null\n"},
		{"numeric scalar", "server:\n  readiness_timeout: 10\n"},
		{"boolean scalar", "redis:\n  username: true\n"},
		{"sequence scalar", "log:\n  level: [secret-sentinel]\n"},
		{"mapping scalar", "redis:\n  address: {secret-sentinel: value}\n"},
		{"nonstring key", "server:\n  123: secret-sentinel\n"},
		{"complex key", "? [secret-sentinel]\n: value\n"},
		{"custom scalar tag", "log:\n  level: !secret-sentinel info\n"},
		{"custom mapping tag", "server: !secret-sentinel {}\n"},
		{"merge key", "server:\n  <<: {listen: '127.0.0.1:8081'}\n"},
		{"malformed yaml", "redis: [secret-sentinel\n"},
		{"extra document", "{}\n---\n{}\n"},
		{"empty extra document", "{}\n---\n"},
		{"malformed extra document", "{}\n---\n[secret-sentinel\n"},
		{"empty listen", "server:\n  listen: ''\n"},
		{"missing listen port", "server:\n  listen: secret-sentinel\n"},
		{"empty port", "server:\n  listen: 'localhost:'\n"},
		{"zero port", "server:\n  listen: 'localhost:0'\n"},
		{"negative port", "server:\n  listen: 'localhost:-1'\n"},
		{"port above range", "redis:\n  address: 'localhost:65536'\n"},
		{"port overflow", "redis:\n  address: 'localhost:99999999999999999999999999'\n"},
		{"named port", "redis:\n  address: 'localhost:redis'\n"},
		{"signed port", "redis:\n  address: 'localhost:+6379'\n"},
		{"empty redis host", "redis:\n  address: ':6379'\n"},
		{"credential url", "redis:\n  address: 'redis://user:secret-sentinel@localhost:6379'\n"},
		{"credential host", "redis:\n  address: 'secret-sentinel@localhost:6379'\n"},
		{"host whitespace", "server:\n  listen: 'secret-sentinel host:8080'\n"},
		{"host path", "redis:\n  address: 'secret-sentinel/host:6379'\n"},
		{"malformed ipv6", "redis:\n  address: '[secret-sentinel::z]:6379'\n"},
		{"unbracketed ipv6", "redis:\n  address: '::1:6379'\n"},
		{"empty readiness timeout", "server:\n  readiness_timeout: ''\n"},
		{"zero readiness timeout", "server:\n  readiness_timeout: 0s\n"},
		{"negative shutdown timeout", "server:\n  shutdown_timeout: -1s\n"},
		{"duration overflow", "server:\n  readiness_timeout: 9223372036854775808ns\n"},
		{"duration without unit", "server:\n  shutdown_timeout: '10'\n"},
		{"invalid duration", "server:\n  shutdown_timeout: secret-sentinel\n"},
		{"empty log level", "log:\n  level: ''\n"},
		{"unsupported log level", "log:\n  level: secret-sentinel\n"},
		{"uppercase log level", "log:\n  level: INFO\n"},
		{"numeric log level", "log:\n  level: 0\n"},
		{"empty password variable", "redis:\n  password_env: ''\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := config.Load(configFile(t, tc.body))
			if err == nil {
				t.Fatal("accepted invalid configuration")
			}
			if strings.Contains(err.Error(), "secret-sentinel") {
				t.Fatal("configuration error disclosed input")
			}
		})
	}
}

func TestLoadResolvesOnlyNamedPassword(t *testing.T) {
	t.Setenv("GATEWAY_CONFIG_TEST_PASSWORD", "secret-sentinel")
	t.Setenv("GATEWAY_CONFIG_TEST_USERNAME", "must-not-expand")
	cfg, err := config.Load(configFile(t, `server:
  listen: "[::1]:18080"
  readiness_timeout: 125ms
  shutdown_timeout: 2m3s
redis:
  address: "redis.internal:16379"
  username: "${GATEWAY_CONFIG_TEST_USERNAME}"
  password_env: GATEWAY_CONFIG_TEST_PASSWORD
log:
  level: debug
`))
	if err != nil {
		t.Fatalf("valid configuration rejected: %v", err)
	}
	if cfg.Server.Listen != "[::1]:18080" || cfg.Server.ReadinessTimeout != 125*time.Millisecond || cfg.Server.ShutdownTimeout != 123*time.Second {
		t.Fatal("server overrides were not preserved")
	}
	if cfg.Redis.Address != "redis.internal:16379" || cfg.Redis.Username != "${GATEWAY_CONFIG_TEST_USERNAME}" || cfg.Redis.Password != "secret-sentinel" {
		t.Fatal("Redis settings did not preserve literals and resolve the named password")
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Fatal("configured log level did not control logging")
	}
}

func TestLoadRequiresNonemptyConfiguredPassword(t *testing.T) {
	const name = "GATEWAY_CONFIG_TEST_MISSING_SECRET_SENTINEL"
	// Setenv restores any original value; Unsetenv then creates a scoped absence.
	t.Setenv(name, "")
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
	path := configFile(t, "redis:\n  password_env: "+name+"\n")
	for _, state := range []string{"absent", "empty"} {
		t.Run(state, func(t *testing.T) {
			if state == "empty" {
				t.Setenv(name, "")
			}
			_, err := config.Load(path)
			if err == nil {
				t.Fatal("accepted a missing or empty configured secret")
			}
			if strings.Contains(err.Error(), name) {
				t.Fatal("configuration error disclosed the environment variable name")
			}
		})
	}
}

func TestLoadOmittedFieldsDoNotRequireCredentials(t *testing.T) {
	cfg, err := config.Load(configFile(t, "server: {}\nredis: {}\nlog: {}\n"))
	if err != nil {
		t.Fatalf("omitted optional values rejected: %v", err)
	}
	if cfg.Redis.Password != "" {
		t.Fatal("credentials were loaded without an explicit password_env")
	}
	if cfg.Server.ReadinessTimeout <= 0 || cfg.Server.ShutdownTimeout <= 0 {
		t.Fatal("omitted timeouts did not produce bounded positive durations")
	}
}

func TestLoadAcceptsAddressAndDurationBoundaries(t *testing.T) {
	cfg, err := config.Load(configFile(t, `server:
  listen: ":1"
  readiness_timeout: 1ns
  shutdown_timeout: 9223372036854775807ns
redis:
  address: "[::1]:65535"
`))
	if err != nil {
		t.Fatalf("valid boundary configuration rejected: %v", err)
	}
	if cfg.Server.ReadinessTimeout != time.Nanosecond || cfg.Server.ShutdownTimeout != time.Duration(9223372036854775807) {
		t.Fatal("duration boundaries were not preserved")
	}
	if cfg.Server.Listen != ":1" || cfg.Redis.Address != "[::1]:65535" {
		t.Fatal("valid wildcard or endpoint port boundary was not preserved")
	}
}

func TestLoadRejectsUnreadablePathWithoutDisclosure(t *testing.T) {
	for _, path := range []string{
		filepath.Join(t.TempDir(), "secret-sentinel.yaml"),
		t.TempDir(),
	} {
		_, err := config.Load(path)
		if err == nil {
			t.Fatal("accepted an unreadable configuration path")
		}
		if strings.Contains(err.Error(), path) || strings.Contains(err.Error(), "secret-sentinel") {
			t.Fatal("configuration error disclosed the input path")
		}
	}
}

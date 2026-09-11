// Package config loads the gateway's strict, single-document YAML configuration.
package config

import (
	"errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

type Config struct {
	Server   Server
	Redis    Redis
	LogLevel slog.Level
}

type Server struct {
	Listen           string
	ReadinessTimeout time.Duration
	ShutdownTimeout  time.Duration
}

type Redis struct {
	Address  string
	Username string
	Password string
}

// Load applies defaults only to omitted fields and resolves only password_env.
// Errors contain trusted field names or categories, never input values or paths.
func Load(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, errors.New("cannot read configuration")
	}
	defer file.Close()

	decoder := yaml.NewDecoder(file)
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return Config{}, errors.New("invalid configuration YAML")
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		return Config{}, errors.New("configuration must contain one YAML document")
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 {
		return Config{}, errors.New("configuration must be a mapping")
	}
	root := document.Content[0]
	if err := mapping(root, "root", "server", "redis", "log"); err != nil {
		return Config{}, err
	}

	cfg := Config{
		Server: Server{
			Listen:           "127.0.0.1:8080",
			ReadinessTimeout: time.Second,
			ShutdownTimeout:  10 * time.Second,
		},
		Redis:    Redis{Address: "127.0.0.1:6379"},
		LogLevel: slog.LevelInfo,
	}
	var passwordEnv string
	for i := 0; i < len(root.Content); i += 2 {
		sectionName, section := root.Content[i].Value, root.Content[i+1]
		switch sectionName {
		case "server":
			err = mapping(section, "server", "listen", "readiness_timeout", "shutdown_timeout")
		case "redis":
			err = mapping(section, "redis", "address", "username", "password_env")
		case "log":
			err = mapping(section, "log", "level")
		}
		if err != nil {
			return Config{}, err
		}
		for j := 0; j < len(section.Content); j += 2 {
			// Both names were checked against the schema before forming the error field.
			field := sectionName + "." + section.Content[j].Value
			node := section.Content[j+1]
			if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
				return Config{}, invalidField(field)
			}
			value := node.Value
			switch field {
			case "server.listen":
				if !validAddress(value, true) {
					return Config{}, invalidField(field)
				}
				cfg.Server.Listen = value
			case "server.readiness_timeout", "server.shutdown_timeout":
				duration, err := time.ParseDuration(value)
				if err != nil || duration <= 0 {
					return Config{}, invalidField(field)
				}
				if field == "server.readiness_timeout" {
					cfg.Server.ReadinessTimeout = duration
				} else {
					cfg.Server.ShutdownTimeout = duration
				}
			case "redis.address":
				if !validAddress(value, false) {
					return Config{}, invalidField(field)
				}
				cfg.Redis.Address = value
			case "redis.username":
				cfg.Redis.Username = value
			case "redis.password_env":
				if value == "" {
					return Config{}, invalidField(field)
				}
				passwordEnv = value
			case "log.level":
				switch value {
				case "debug":
					cfg.LogLevel = slog.LevelDebug
				case "info":
					cfg.LogLevel = slog.LevelInfo
				case "warn":
					cfg.LogLevel = slog.LevelWarn
				case "error":
					cfg.LogLevel = slog.LevelError
				default:
					return Config{}, invalidField(field)
				}
			}
		}
	}
	if passwordEnv != "" {
		password, ok := os.LookupEnv(passwordEnv)
		if !ok || password == "" {
			return Config{}, errors.New("redis.password_env requires a nonempty environment secret")
		}
		cfg.Redis.Password = password
	}
	return cfg, nil
}

func invalidField(field string) error {
	return errors.New("invalid configuration field: " + field)
}

// mapping validates shape, key types, unknown fields, and duplicates before any
// input key may be used in an error. Each schema mapping has at most three keys.
func mapping(node *yaml.Node, field string, allowed ...string) error {
	if node.Kind != yaml.MappingNode || node.Tag != "!!map" {
		return invalidField(field)
	}
	var seen uint
	for i := 0; i < len(node.Content); i += 2 {
		key := node.Content[i]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
			return invalidField(field)
		}
		var bit uint
		for index, name := range allowed {
			if key.Value == name {
				bit = 1 << index
				break
			}
		}
		if bit == 0 || seen&bit != 0 {
			return invalidField(field)
		}
		seen |= bit
	}
	return nil
}

func validAddress(value string, allowEmptyHost bool) bool {
	host, port, err := net.SplitHostPort(value)
	if err != nil || port == "" {
		return false
	}
	for _, digit := range port {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	number, err := strconv.ParseUint(port, 10, 16)
	if err != nil || number == 0 {
		return false
	}
	if host == "" {
		return allowEmptyHost
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return true
	}
	// Brackets are reserved for IP literals, not arbitrary host names.
	if strings.HasPrefix(value, "[") {
		return false
	}
	host = strings.TrimSuffix(host, ".")
	if len(host) == 0 || len(host) > 253 {
		return false
	}
	for label := range strings.SplitSeq(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, char := range label {
			if char != '-' && (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') {
				return false
			}
		}
	}
	return true
}

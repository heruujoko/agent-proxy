# Service Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement T05/T06 as a runnable, configurable Go gateway foundation with real Redis readiness, graceful shutdown, and the subsequently requested deployable gateway image.

**Architecture:** Standard-library HTTP and structured logging, strict YAML configuration, and one shared Redis client. Readiness performs a bounded check per request; Redis failure never kills liveness. A multi-stage Dockerfile produces a non-root gateway image; it does not deploy Redis or Hermes.

**Tech Stack:** Go 1.27.1, `github.com/gomodule/redigo v1.9.3`, `go.yaml.in/yaml/v3 v3.0.5`, Docker, isolated Redis 8.2.1 fixture.

**Spec:** [Approved service design](../specs/2026-09-11-service-foundation-design.md). The user's subsequent Dockerfile request supersedes the gateway-image exclusion only; Compose/Hermes packaging remains deferred.

## Global Constraints

- One MR on `feat/gateway-service-foundation`; no new worktree, push, merge, or publication without authorization.
- Require an explicit `--config <path>` argument. No implicit config-file discovery, hot reload, general environment interpolation, or integration-specific configuration is added.
- HTTP listen default `127.0.0.1:8080`; Redis default `127.0.0.1:6379`; readiness timeout `1s`; shutdown timeout `10s`; log level `info`.
- Start alive but unready during Redis failure; invalid configuration or listener-bind failure exits nonzero.
- Passwords come only from the environment variable named by `redis.password_env`; missing or empty configured secrets fail startup.
- Strictly reject unknown fields, duplicate keys, extra YAML documents, invalid types, null values, malformed addresses, invalid durations, and unsupported log levels. Defaults apply to omitted fields, not explicitly invalid values.
- JSON logs go to stderr. Neither config errors, upstream errors, nor health responses may expose secret values or raw configuration.
- Readiness has one total deadline including dialing, pool waiting, handshake, and retries. Request cancellation must not yield ready status.
- Shutdown marks stopping before HTTP draining, then closes Redis. Deadline failure force-closes HTTP and exits nonzero.
- No admission/model/session/run path, generic repository/audit framework, or production Compose topology.
- Tests defend observable contracts. Real Redis is disposable and isolated; never flush existing databases.
- Concurrent implementers edit disjoint files and skip all formatting/build/lint/test commands. The integration owner runs validation after the wave is joined. No worker commits or launches additional agents.
- Native Go and Redis are absent from PATH. The user authorized scoped sudo Docker operations only; no socket/group changes or host package installation. Authentication is through a system password dialog, never chat.

---

## Verified dependencies and runtime

Version metadata was read from [Go downloads](https://go.dev/dl/?mode=json), [Redigo module metadata](https://proxy.golang.org/github.com/gomodule/redigo/@v/v1.9.3.mod), and [YAML module metadata](https://proxy.golang.org/go.yaml.in/yaml/v3/@v/v3.0.5.mod). Redigo requires Go 1.17 or newer; selected Go 1.27.1 meets that constraint.

Pinned build image:

```text
golang:1.27.1-bookworm@sha256:648f440f42a0958804efb24df176f806f9d353b41f1c0627f666428e40310f6b
```

Pinned disposable Redis image:

```text
redis:8.2.1-alpine@sha256:987c376c727652f99625c7d205a1cba3cb2c53b92b0b62aade2bd48ee1593232
```

The Docker daemon was verified through the authorized sudo path: `29.7.2 linux x86_64`. Use user-owned bind mounts and `--user 1000:1000` for this workstation's Go container so generated files are not root-owned. README commands must use the caller's UID/GID rather than hard-code this workstation. Use per-container writable module/build caches under `/tmp`; no Docker socket mount inside a container.

For interactive sessions, launch long-running build/runtime/Redis containers through the process supervisor. Only finite `docker exec`, build, inspect, and stop/remove operations use ordinary command execution. A compiler container may stay alive for the duration of validation; remove it afterward.

## File ownership and interfaces

| Owner | Files | Responsibility |
| --- | --- | --- |
| Integration owner | `go.mod`, `go.sum` | Exact module/dependency pins and dependency resolution |
| Configuration worker | `internal/config/config.go`, `internal/config/config_test.go`, `config/config.example.yaml` | Strict schema, defaults, secrets, safe validation errors |
| Health worker | `internal/server/health.go`, `internal/server/health_test.go` | Health routes, deadline/cancellation behavior, stopping readiness |
| Integration owner | `internal/server/lifecycle.go`, `internal/server/lifecycle_test.go`, `cmd/gateway/main.go`, `cmd/gateway/main_test.go` | Server drain, Redis client configuration, real executable and process checks |
| Image worker | `Dockerfile`, `.dockerignore`, `config/config.container.example.yaml` | Static non-root runtime image and container-safe sample |
| Integration owner | `README.md`, existing plan/spec status and evidence | Exercised development, verification, container operation, limitations |

Freeze these signatures before concurrent implementation:

```go
// package config
// Public values are consumed by main; only Load owns YAML parsing.
type Config struct {
    Server Server
    Redis Redis
    LogLevel slog.Level
}
type Server struct {
    Listen string
    ReadinessTimeout time.Duration
    ShutdownTimeout time.Duration
}
type Redis struct {
    Address string
    Username string
    Password string
}
func Load(path string) (Config, error)

// package server
// The checker is the external dependency seam, not an alternate backend.
func NewHealth(check func(context.Context) error, timeout time.Duration) *Health
func (h *Health) Handler() http.Handler
func (h *Health) Stop()

// Lifecycle integration; main creates the listener before logging startup.
func Serve(ctx context.Context, srv *http.Server, listener net.Listener,
    health *Health, shutdownTimeout time.Duration) error

// package main; permits startup failure checks without os.Exit inside run.
func run(ctx context.Context, args []string, stderr io.Writer) int
```

`Health` owns an atomic stopping flag; callers cannot reset it. `Serve` owns HTTP serving/draining, not the Redis client. `run` owns and closes Redis only after `Serve` returns. Config tests use real temporary files and scoped environment values. Readiness tests inject only the external checker; they assert HTTP results and bounded completion, not that mocks exist.

## Task 1: Strict configuration and runnable local example

**Files:** Create `internal/config/config.go`, `internal/config/config_test.go`, `config/config.example.yaml`; integration owner creates module files.

**Consumes:** Explicit config path and `os.LookupEnv`.
**Produces:** The exact `config.Load`, `Config`, `Server`, and `Redis` contract above.

- [ ] **Step 1: Initialize pinned module metadata and write the rejection regression checks before the implementation.**

```go
module github.com/heruujoko/agent-proxy

go 1.27.1

require (
    github.com/gomodule/redigo v1.9.3
    go.yaml.in/yaml/v3 v3.0.5
)
```

Use independently specified rejection fixtures; the following pattern is concrete test code for the config worker to expand with the remaining schema boundaries:

```go
func TestLoadRejectsInvalidInputWithoutDisclosure(t *testing.T) {
    for _, body := range []string{
        "redis:\n  password: secret-sentinel\n",
        "server:\n  listen: secret-sentinel\n",
        "server:\n  readiness_timeout: 0s\n",
        "log:\n  level: secret-sentinel\n",
        "redis:\n  address: localhost:6379\n  address: localhost:6380\n",
        "server: null\n",
        "{}\n---\n{}\n",
        "\n",
    } {
        t.Run(body, func(t *testing.T) {
            path := filepath.Join(t.TempDir(), "config.yaml")
            if err := os.WriteFile(path, []byte(body), 0600); err != nil {
                t.Fatal(err)
            }
            _, err := Load(path)
            if err == nil { t.Fatal("accepted invalid configuration") }
            if strings.Contains(err.Error(), "secret-sentinel") {
                t.Fatal("configuration error disclosed input")
            }
        })
    }
}
```

Add separate behavior checks for configured missing/empty password variables, valid explicit overrides, unknown nested fields, wrong scalar types, invalid/empty ports, duration overflow, omitted fields versus explicit empty/null values, unreadable files, and extra documents. Do not assert a struct merely equals a duplicate list of defaults.

- [ ] **Step 2: Implement strict parsing and safe normalization.**

Use the parser's YAML node representation to distinguish omitted keys from explicit nulls, validate root/section mapping shapes, and detect duplicates before mapping to typed values. Alternatively use strict decoding with a node validation pass; never let `Node.Decode` silently bypass unknown-field checks. Reject raw YAML errors with a safe category rather than echoing parser text.

Implement address and duration validation along these lines, with error messages containing only trusted field names:

```go
func validAddress(value string) bool {
    host, port, err := net.SplitHostPort(value)
    if err != nil || strings.ContainsAny(host, " \t\r\n/@") { return false }
    n, err := strconv.Atoi(port)
    return err == nil && n >= 1 && n <= 65535
}

func positiveDuration(value string) (time.Duration, error) {
    d, err := time.ParseDuration(value)
    if err != nil || d <= 0 { return 0, errors.New("invalid duration") }
    return d, nil
}
```

Reject empty Redis host names; allow explicit HTTP wildcard host forms accepted by Go's listener. Resolve only `password_env`, using `os.LookupEnv`; never interpolate arbitrary values or return `PathError`/parser errors carrying raw user input. Accept log levels with the spec's exact lower-case spelling. Return `slog.Level` to main.

- [ ] **Step 3: Create the runnable local example.**

```yaml
server:
  listen: "127.0.0.1:8080"
  readiness_timeout: 1s
  shutdown_timeout: 10s
redis:
  address: "127.0.0.1:6379"
log:
  level: info
```

- [ ] **Step 4: Integration owner runs config checks after the concurrent write wave.**

```text
go test ./internal/config -count=1
```

Expected: accepted valid configuration, all malformed/secret-bearing fixtures rejected without disclosure. No credential or inline input appears in error output. Record observed results, not just the command.

## Task 2: Bounded health routes with shutdown readiness

**Files:** Create `internal/server/health.go`, `internal/server/health_test.go`.

**Consumes:** Context-aware checker and readiness duration; no config-package dependency.
**Produces:** `NewHealth`, `Handler`, and `Stop` above.

- [ ] **Step 1: Write HTTP behavior tests before handlers.**

```go
func TestHealthRemainsLiveWhenDependencyFails(t *testing.T) {
    h := NewHealth(func(context.Context) error {
        return errors.New("secret-sentinel")
    }, time.Second)
    for _, tc := range []struct{ path string; want int }{
        {"/healthz", http.StatusOK},
        {"/readyz", http.StatusServiceUnavailable},
    } {
        w := httptest.NewRecorder()
        h.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
        if w.Code != tc.want { t.Fatalf("%s: got %d, want %d", tc.path, w.Code, tc.want) }
        if strings.Contains(w.Body.String(), "secret-sentinel") {
            t.Fatal("health response disclosed upstream error")
        }
    }
}
```

Add separate checks for available Redis, timeout, already-cancelled request, cancellation during a check, Stop during an in-flight successful check, and simultaneous Stop/readiness requests. Synchronize ordering with channels, not fixed sleeps. Assert generic HTTP status, not exact body text. Unknown paths must not inherit the liveness route.

- [ ] **Step 2: Implement the two routes and atomic stopping state.**

Core flow:

```go
ctx, cancel := context.WithTimeout(r.Context(), h.timeout)
defer cancel()
if h.stopping.Load() || ctx.Err() != nil {
    http.Error(w, "not ready", http.StatusServiceUnavailable)
    return
}
err := h.check(ctx)
if err != nil || ctx.Err() != nil || h.stopping.Load() {
    http.Error(w, "not ready", http.StatusServiceUnavailable)
    return
}
w.WriteHeader(http.StatusOK)
```

The checker must actually honor the total deadline; task 3 proves the selected Redis client does so on the wire. Do not introduce a cached readiness flag, polling loop, unbounded background checks, raw errors in bodies, or a fake admission gate.

- [ ] **Step 3: Integration owner verifies after the write wave.**

```text
go test ./internal/server -count=1
```

Expected: correct liveness/readiness status, deadline/cancellation failures, and no ready result after the handler observes stopping. Race tests run after task 3 integrates lifecycle.

## Task 3: Real executable, Redis client, and HTTP draining

**Files:** Create `internal/server/lifecycle.go`, `internal/server/lifecycle_test.go`, `cmd/gateway/main.go`, `cmd/gateway/main_test.go`; resolve `go.sum`.

**Consumes:** Tasks 1/2 interfaces.
**Produces:** Runnable `gateway --config <path>` binary and `server.Serve` lifecycle.

- [ ] **Step 1: Add executable failure tests before integration.**

```go
func TestRunRejectsInvalidArgumentsWithoutDisclosure(t *testing.T) {
    for _, args := range [][]string{
        {}, {"--unknown-secret-sentinel"},
        {"--config", "missing-secret-sentinel.yaml"},
    } {
        var output bytes.Buffer
        if code := run(context.Background(), args, &output); code == 0 {
            t.Fatal("invalid startup succeeded")
        }
        if strings.Contains(output.String(), "secret-sentinel") {
            t.Fatal("startup disclosed input")
        }
    }
}
```

Also bind a real ephemeral listener, write its address into a temp config, and prove the second bind exits nonzero. For lifecycle tests, use real listeners and a test-only controlled blocking handler: request enters -> cancel service context -> release handler -> verify successful drain. A separate blocked handler remains active past a short configured deadline; verify `Serve` returns an error and the connection closes. Release all test-owned resources even on failure.

- [ ] **Step 2: Implement explicit flag parsing, JSON logging, and shared Redis construction.**

Use `flag.NewFlagSet` with `flag.ContinueOnError`, discard the parser's raw diagnostic writer, reject missing config path/extra positional args, and log safe startup categories. `main` uses `signal.NotifyContext` for SIGINT/SIGTERM and exits with `run`'s status after cleanup.

Configure the real Redis client with the following intent:

```go
pool := &redis.Pool{
    MaxIdle: 2,
    MaxActive: 8,
    Wait: true,
    DialContext: func(ctx context.Context) (redis.Conn, error) {
        conn, err := redis.DialContext(ctx, "tcp", cfg.Redis.Address,
            redis.DialConnectTimeout(cfg.Server.ReadinessTimeout),
            redis.DialReadTimeout(cfg.Server.ReadinessTimeout),
            redis.DialWriteTimeout(cfg.Server.ReadinessTimeout))
        if err != nil { return nil, err }
        switch {
        case cfg.Redis.Username != "":
            _, err = redis.DoContext(conn, ctx, "AUTH", cfg.Redis.Username, cfg.Redis.Password)
        case cfg.Redis.Password != "":
            _, err = redis.DoContext(conn, ctx, "AUTH", cfg.Redis.Password)
        }
        if err != nil {
            conn.Close()
            return nil, err
        }
        return conn, nil
    },
}
health := server.NewHealth(func(ctx context.Context) error {
    conn, err := pool.GetContext(ctx)
    if err != nil { return err }
    defer conn.Close()
    _, err = redis.DoContext(conn, ctx, "PING")
    return err
}, cfg.Server.ReadinessTimeout)
```

Use one bounded shared pool: at most eight active readiness connections, with up to two idle connections retained. Pool waiting, dialing/authentication, and `PING` all receive the request context; no implicit retry loop extends the check. Return only generic HTTP errors and safe diagnostic categories. Never print Redis options or the config struct.

**Review ruling:** Replace the initially selected go-redis client with Redigo v1.9.3. Source inspection confirmed go-redis's deadline option does not interrupt an in-flight socket read on context cancellation; Redigo's `DoContext` closes only the affected connection. This preserves the spec without custom connection tracking or closing the shared client. Add a silent-peer regression that cancels after command bytes reach the socket and requires both prompt handler return and peer-observed connection closure. Cost of a wrong selection: dependency/client integration rework; no external gateway API changes.

**Credential correction:** An explicitly configured username must never fall back to the default Redis account. Redigo's dial options omit authentication when the password is empty, so authenticate explicitly with `DoContext` after dialing. A wire-level regression reproduced the incorrect `200` before this correction; it must return `503` for an invalid named user even when the server's default account allows unauthenticated `PING`.

No initial `PING` is a startup gate. Bind the listener explicitly, then log that the listener is available, not that Redis is ready. Use bounded HTTP read-header/read/write/idle timeouts; select 5-second headers, 60-second idle, and read/write budgets at least as long as the readiness budget plus a safe response margin, with overflow-safe duration handling.

- [ ] **Step 3: Implement the HTTP lifecycle and dependency ordering.**

`Serve` runs `http.Server.Serve` with a buffered result channel. On cancellation, call `health.Stop()` before `Shutdown` using a fresh background context bounded by shutdown timeout. On drain failure, call `Close` and return a safe error. Normalize `http.ErrServerClosed` only for expected shutdown; unexpected serving failure is nonzero. `run` closes Redis after `Serve` returns, including failures; no defer is bypassed by `os.Exit` inside `run`.

- [ ] **Step 4: Add an actual Redis wire timeout characterization.**

Use a test-owned TCP listener that accepts but never answers to prove dialing/handshake/readiness cannot outlive the configured total budget. Use a request deadline shorter than the configured client timeout and prove it wins. Never assert only that timeout option fields were assigned. Use isolated real Redis for successful authentication/`PING`, outage/recovery, and invalid authentication. Credentials remain fixture-local.

- [ ] **Step 5: Run integrated checks and compile the binary.**

```text
go mod tidy
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
go build -o /tmp/gateway ./cmd/gateway
```

Use the pinned container toolchain for these commands. Check observable process SIGTERM exit on the built binary, not only cancellation of a function in a unit test.

## Task 4: Deployable gateway image and real-process verification

**Files:** Create `Dockerfile`, `.dockerignore`, `config/config.container.example.yaml`; update `README.md` after smoke proof. No Compose or Hermes files.

**Consumes:** Module metadata and `./cmd/gateway`; config schema from task 1.
**Produces:** Non-root gateway image with runtime-mounted YAML configuration.

- [ ] **Step 1: Add the requested multi-stage image.**

```dockerfile
FROM golang:1.27.1-bookworm@sha256:648f440f42a0958804efb24df176f806f9d353b41f1c0627f666428e40310f6b AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /gateway ./cmd/gateway

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /gateway /gateway
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/gateway"]
CMD ["--config", "/etc/gateway/config.yaml"]
```

Use a narrow Docker build context: allow only module files and `cmd/`/`internal/` source; exclude `.git`, docs, temporary files, environment secrets, and config files. The runtime image contains no baked credentials or YAML, shell, compiler, package manager, or Redis process. The CA bundle supports ordinary secure client connections without expanding this MR into a TLS configuration feature.

- [ ] **Step 2: Add the container example.**

```yaml
server:
  listen: "0.0.0.0:8080"
  readiness_timeout: 1s
  shutdown_timeout: 10s
redis:
  address: "redis:6379"
log:
  level: info
```

A user-defined Docker network supplies the `redis` DNS name. Mount the YAML read-only at `/etc/gateway/config.yaml`; ensure UID 65532 can read it. A password deployment adds `password_env` and provides that variable at runtime, never as a build argument.

- [ ] **Step 3: Build and smoke the actual image against disposable Redis.**

```text
docker build -t agent-proxy:foundation .
```

Create only task-owned containers/network. Publish the gateway HTTP port on host loopback only. Start the gateway image before Redis, observe liveness 200/readiness 503, then bring Redis up and observe readiness 200 with the same gateway process. Stop Redis, verify liveness 200/readiness 503 within budget, restart it, and recover readiness without restarting the gateway. Send SIGTERM and require exit 0. Inspect the runtime UID and verify operation with a read-only root filesystem. Check bad credentials yield generic readiness failure without leaking the supplied sentinel in logs.

The process supervisor owns long-running containers; do not use unsupervised background services. Run only finite Docker commands directly. Record actual container/image identities and results so cleanup cannot target an unrelated resource.

- [ ] **Step 4: Update README with commands actually exercised and correct current capabilities.**

Keep the PRD link and future architecture, but state that this MR implements operational foundation only. Document native-Go commands, the pinned compiler-container equivalent, both YAML examples, env-secret behavior, Docker build/run with read-only config/rootfs and loopback port publishing, readiness semantics, safe shutdown, and current absence of Discord/Hermes execution. Docker image operation is now included; Compose/Hermes remains T24.

- [ ] **Step 5: Review and clean up only after smoke proof.**

Run one integrated formatter/test/race/vet/build pass and the actual image smoke. Request task-scoped reviews for configuration and health/lifecycle plus whole-change review. Fix evidenced findings and rerun affected checks. Remove disposable containers/network, temporary askpass helper, scratch verification artifacts, and throwaway scripts. Do not delete unrelated data or images. Update task/spec status with exactly the exercised evidence; do not mark downstream gateway acceptance complete.

## Review map and execution ordering

Tasks 1 and 2 are independent after the signatures above are fixed; task 4's image files can be authored alongside them but cannot be accepted until task 3 builds and the image smoke passes. Main owns module metadata, lifecycle/main integration, dependency resolution, validation, and final docs. Configuration and health workers do not mutate each other's files. Image worker owns only its three listed files.

| Boundary | Producer / consumer | Review invariant |
| --- | --- | --- |
| Task 1 -> Task 3 | `config.Load`, typed Config | No raw YAML or secrets reach startup diagnostics; exact fields match |
| Task 2 -> Task 3 | `NewHealth`, `Handler`, `Stop` | Stop precedes drain; Redis lives until draining completes |
| Task 3 -> Task 4 | `./cmd/gateway`, `--config` | Static non-root binary, readable mounted config, no default startup Redis dependency |
| Task 1 -> Task 4 | YAML schema | Local bind remains loopback; container example explicitly binds all container interfaces |
| Task 1 | Strict config/tests/example | Test fixtures match the schema; defaults never rescue explicit invalid values |
| Task 2 | Health handlers/tests | Assertions cover HTTP outcomes and deadlines, not mock plumbing |
| Task 3 | Real client/lifecycle/tests | Client options are verified on the wire; process exit and cleanup match contract |
| Task 4 | Dockerfile/example/README | Image actually runs; command examples use verified networking and UID permissions |

**Execution ruling:** The user chose subagents. Use parallel, disjoint implementation ownership with main integration and a joined validation pass; no worker launches tests or commits mid-wave. This follows the harness's concurrency rules rather than serializing unrelated slices solely to satisfy a generic skill template. Record failures and verification honestly; no unobserved test-first claim.

**Acceptance map:** Spec §§1–3 -> task 1; §§2/4 -> task 2; §§4–6 -> task 3; §7 -> tasks 1–4 verification; §8 -> README/task-status updates; §9 -> integrated review and proof. User's Dockerfile addition -> task 4. No required admission/run acceptance has been reassigned to a readiness-only test.

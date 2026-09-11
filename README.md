# Agent Access Gateway for Hermes

A Go control plane for the planned Discord-to-Hermes gateway.

**Implemented: T05/T06 service foundation.** The service loads strict YAML configuration, logs JSON to stderr, checks Redis readiness, handles graceful shutdown, and runs in a minimal non-root Docker image.

Discord ingress, identity/policy enforcement, rate limiting, Hermes execution, sessions, and progress delivery are **not implemented yet**. The architecture below describes the target system, not current runtime capabilities.

---

## Run with Docker

Requires Docker daemon access. Prefix Docker commands with `sudo` if your installation requires it; no host Go or Redis installation is necessary.

```sh
docker build --progress=plain -t agent-proxy:foundation .
docker network create gateway-dev
docker run -d --name redis --network gateway-dev \
  redis:8.2.1-alpine@sha256:987c376c727652f99625c7d205a1cba3cb2c53b92b0b62aade2bd48ee1593232 \
  redis-server --save "" --appendonly no
docker run -d --name gateway --network gateway-dev \
  --read-only --cap-drop ALL --security-opt no-new-privileges \
  -p 127.0.0.1:8080:8080 \
  --mount "type=bind,source=$PWD/config/config.container.example.yaml,target=/etc/gateway/config.yaml,readonly" \
  agent-proxy:foundation
```

This is an isolated development Redis without persistence or authentication, not a production Redis deployment. Redis has no published host port; only gateway health endpoints are published on host loopback. Use a normal user-defined bridge: Docker's `--internal` network did not publish the gateway port during verification.

The image runs as UID/GID `65532:65532`, contains a static binary and CA roots, and has no shell or baked-in configuration/secrets. Its default command is `/gateway --config /etc/gateway/config.yaml`. The mounted configuration must be readable by that UID. The container example explicitly binds `0.0.0.0:8080` inside the container and resolves Redis at `redis:6379`.

```sh
curl -i http://127.0.0.1:8080/healthz
curl -i http://127.0.0.1:8080/readyz
docker stop redis
curl -i http://127.0.0.1:8080/readyz
docker start redis
curl -i http://127.0.0.1:8080/readyz
docker stop --timeout 15 gateway
docker logs gateway
```

Starting the gateway before Redis is also supported: liveness succeeds, readiness fails, and readiness recovers when Redis appears. Gateway restart is not required.

Remove only the development resources created above when finished:

```sh
docker rm gateway
docker stop redis
docker rm redis
docker network rm gateway-dev
```

Compose topology, persistent Redis provisioning, and Hermes packaging remain later work.

## Configuration and lifecycle

Pass an explicit `--config` YAML path. There is no implicit file discovery, hot reload, or general environment interpolation.

| Setting | Default / behavior |
| --- | --- |
| `server.listen` | `127.0.0.1:8080`; override explicitly for container access |
| `server.readiness_timeout` | `1s`; total Redis check budget |
| `server.shutdown_timeout` | `10s`; HTTP drain budget |
| `redis.address` | `127.0.0.1:6379`; host and port, not a connection URL |
| `redis.username` | Optional ACL username; if supplied, it is always used for authentication |
| `redis.password_env` | Optional environment variable name; the named value must exist and be nonempty |
| `log.level` | `info`; also accepts `debug`, `warn`, `error` |

Defaults apply to omitted fields only. Unknown fields, duplicate keys, nulls, extra YAML documents, invalid types/addresses/durations/levels, and empty configuration files are rejected. YAML aliases and merge keys are not supported.

For authenticated Redis, add `password_env: GATEWAY_REDIS_PASSWORD` under `redis` and supply that environment variable from your secret manager or a protected environment file. Docker accepts `--env GATEWAY_REDIS_PASSWORD` or `--env-file`; do not use a build argument, inline YAML password, or commit the environment file. Add `username` when using an ACL user. A named user with no password variable attempts authentication with an empty password; it never silently falls back to the default account.

| State | `GET /healthz` | `GET /readyz` |
| --- | --- | --- |
| Redis available and authenticated | `200` | `200` |
| Redis missing, disconnected, timed out, or authentication rejected | `200` | `503` |
| Shutdown observed by a request still being served | `200` | `503` |

Readiness uses a bounded, context-aware Redis `PING`, not cached state. Cancelling a request interrupts its leased Redis connection without closing the shared pool. Success proves that operation's connectivity/authentication, not all Redis permissions, durability, or future admission safety.

Invalid arguments/configuration and listener-bind failures exit nonzero. On SIGINT/SIGTERM, readiness is withdrawn, HTTP stops accepting connections and drains, then Redis closes. Successful shutdown exits `0`; drain failure forces connection closure and exits nonzero. There is no artificial delay to make the unready shutdown interval externally observable.

Logs are JSON on stderr. Errors report safe categories rather than raw configuration, credentials, or upstream error text. Redis readiness failures are visible at `debug` level.

## Build and verify

Dependencies are pinned in `go.mod`/`go.sum`: Go 1.27.1, Redigo 1.9.3, and `go.yaml.in/yaml/v3` 3.0.5. With that Go toolchain:

```sh
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
go build -o /tmp/gateway ./cmd/gateway
/tmp/gateway --config config/config.example.yaml
```

The Go checks were verified using the pinned compiler container, not a host Go installation:

```sh
docker run --rm --user "$(id -u):$(id -g)" \
  -e GOCACHE=/tmp/go-cache -e GOMODCACHE=/tmp/go-mod \
  -v "$PWD:/src:ro" -w /src \
  golang:1.27.1-bookworm@sha256:648f440f42a0958804efb24df176f806f9d353b41f1c0627f666428e40310f6b \
  sh -c 'go test ./... -count=1 && go test -race ./... -count=1 && go vet ./... && go build -o /tmp/gateway ./cmd/gateway'
```

Tests cover strict configuration and secret-safe failures; real local TCP deadline/cancellation and credential boundaries; liveness/readiness; HTTP drain and forced-close behavior; invalid startup and bind failures. They do not require a shared Redis database or external credentials.

Actual-image smoke verification additionally exercises isolated real Redis authentication, outage/recovery without gateway restart, non-root/read-only operation, SIGTERM, startup failure exit codes, and captured-log credential redaction. No Discord/Hermes acceptance is claimed.

---

## Planned Architecture

```text
                       Platform (Discord / Telegram)
                                    |
                                    v
                         +--------------------+
                         |   Agent Gateway    | (Go - Stateless)
                         +---------+----------+
                                   |
        +--------------------------+--------------------------+
        |                          |                          |
        v                          v                          v
 Identity / Rate Limit     Validation & Intent        Policy Enforcement
 (Redis token bucket)       (LLM Verifier)             (Deterministic)
                                   |
                                   v
                         Session & Run Manager
                                   |
                    +--------------+--------------+
                    |                             |
                    v                             v
           Hermes Agent API              Redis State Store
           (Run execution / SSE)         (Sessions, Runs, Locks)
                    |
                    v
           Event Stream Relay
                    |
                    v
            Platform Renderer
```

### Target Design Principles

1. **Stateless Gateway**: No critical state in process memory. All coordination, rate-limiting, session mapping, and active run states live in Redis. Horizontally scalable ($N$ Gateways $\rightarrow$ 1 Hermes).
2. **Immutable Input**: Original user inputs are preserved verbatim. Interceptors never rewrite or mutate user prompts.
3. **LLM Classifies, Policy Authorizes**: A lightweight model classifies intent and risk; deterministic policy decides whether to allow, deny, or escalate.
4. **Decoupled Run Lifecycle**: Runs are asynchronous jobs tracked via run IDs and event streams (SSE/WebSocket), never tied to short HTTP request timeouts.
5. **Safe Progress Relay**: Operational status and tool events are relayed back to the chat platform without exposing private chain-of-thought.

---

## Planned Request Flow

1. **Ingress**: Platform adapter (Discord/Telegram) receives an event and creates a canonical request envelope preserving raw text.
2. **Identity & Rate Limiting**: Resolves actor identity and applies Redis-backed token-bucket limits.
3. **Validation & Intent Verification**: Runs deterministic checks and passes payload to a structured LLM verifier for intent/risk classification.
4. **Policy Evaluation**: Deterministic rules evaluate the classification against actor roles/permissions.
5. **Run Creation & Session Mapping**: Resolves/creates the Hermes conversation and triggers a decoupled run.
6. **Event Subscription & Relay**: Subscribes to Hermes SSE run events (status, tool calls, completion) and renders updates to Discord/Telegram.

---

## Specification

Detailed architecture, data contracts, and implementation requirements are documented in the specification:

- [Agent Access Gateway PRD](spec/agent-access-gateway-hermes-prd.md)
- [T05/T06 service design](docs/superpowers/specs/2026-09-11-service-foundation-design.md)
- [Service implementation plan and evidence](docs/superpowers/plans/2026-09-11-service-foundation.md)
- [Gateway task breakdown](docs/plans/2026-09-11-gateway-tasks.md)

---

## Tech Stack

- **Language:** Go 1.27.1
- **Current dependencies:** Redigo, YAML v3, Redis
- **Runtime image:** Static non-root gateway built with a pinned Go image
- **Planned integrations:** Discord first, Hermes backend; Telegram later

---

## License

[MIT](LICENSE) © 2026 Heru Joko P. Utomo

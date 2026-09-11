# T05 + T06: Go Service Foundation Design

**Date:** 2026-09-11
**Branch:** `feat/gateway-service-foundation`
**Status:** Design sections approved in conversation; written-spec review pending. Implementation has not started.
**Scope:** One MR combining T05 and T06 from the [task breakdown](../../plans/2026-09-11-gateway-tasks.md).
**References:** [System design](../../plans/2026-09-10-gateway-design.md), [milestone plan](../../plans/2026-09-10-gateway-plan.md), [PRD](../../../spec/agent-access-gateway-hermes-prd.md).

## 1. Goal and boundary

Deliver a runnable Go service with strict configuration, structured logging, a real Redis client, HTTP liveness/readiness, and graceful shutdown. It must remain alive but unready when Redis is unavailable and recover readiness without restarting when Redis returns.

This is an operational foundation, not a Discord-to-Hermes gateway yet. No request-admission endpoint, model client, session store, run manager, queue, or fake execution path is included. Passing readiness is not evidence of admission enforcement or successful execution.

Included artifacts are the Go module, service implementation, focused regression tests, isolated real-Redis verification support, example configuration, README instructions, this design, and the subsequent implementation plan. Existing task descriptions must agree with the acceptance ownership in section 8.

Excluded: Discord/Hermes/verifier integrations and settings; identity/capability configuration; generic repository or audit frameworks; production Dockerfile/Compose packaging; host package installation; unrelated refactoring. Production packaging remains T24. No empty packages or interfaces for future features.

## 2. Architecture and alternatives

Use Go standard-library `net/http`, `slog`, signal handling, and testing, plus a maintained Redis client and YAML parser. The implementation plan selects and pins supported Go and dependency versions before code is written; no dependency version is asserted as installed by this design.

One process owns one shared Redis client and one HTTP server. Construct only the components used in this MR.

| Location | Responsibility | Dependencies |
| --- | --- | --- |
| `cmd/gateway/main.go` | Parse the config-path flag, construct logging/client/server, handle signals, coordinate shutdown and exit status | Configuration and HTTP packages, Redis client |
| `internal/config/config.go` | Decode and validate configuration; resolve explicitly named environment secrets without disclosing their contents | YAML parser, standard library |
| `internal/server/health.go` | Expose liveness/readiness handlers, apply bounded Redis checks, observe stopping state | Standard library and the minimal Redis check needed by the handlers |
| `internal/server/` as needed | HTTP server setup and focused lifecycle behavior; no generic server framework | Standard library |
| `config/config.example.yaml` | Runnable local-development settings with no committed secret values | None |

Keep logger construction simple and use `slog` directly. Do not create `internal/audit` merely to wrap the standard library. Full correlated request/run audit ownership remains T23.

### Readiness alternatives

1. **Bounded Redis check per readiness request — selected.** No background poller or cached readiness. Each request observes dependency availability with its own deadline. The small probe cost is appropriate for this foundation.
2. **Background polling and cached readiness — rejected for this MR.** Cheaper HTTP responses but introduces stale results, polling policy, and another goroutine to drain without a demonstrated need.

### Startup alternatives

1. **Start alive but unready during Redis failure — selected.** Supports independent service startup and recovery without dependency-induced restart loops.
2. **Exit when initial Redis connection fails — rejected.** Requires supervisor/operator restarts for a recoverable dependency outage.

Configuration and listener-bind errors still fail startup immediately. They are not treated as transient Redis failures.

## 3. Configuration contract

The command requires an explicit `--config <path>` argument. No implicit config-file discovery, hot reload, general environment interpolation, or integration-specific configuration is added. The example can be used directly for local development:

```text
go run ./cmd/gateway --config config/config.example.yaml
```

This is an intended post-implementation command, not a command already executed successfully.

Use a single YAML mapping with the following settings. Omitted optional fields receive their defaults; explicitly supplied invalid values are rejected rather than silently replaced. An empty file is not a valid configuration.

| Field | Default | Validation / meaning |
| --- | --- | --- |
| `server.listen` | `127.0.0.1:8080` | Nonempty host-and-port address with port 1–65535; wildcard binding must be explicit |
| `server.readiness_timeout` | `1s` | Positive Go duration; bounds the complete Redis readiness operation |
| `server.shutdown_timeout` | `10s` | Positive Go duration; bounds HTTP draining |
| `redis.address` | `127.0.0.1:6379` | Nonempty host-and-port address with port 1–65535, not a credential-bearing URL |
| `redis.username` | Omitted | Optional Redis ACL username; no user/role authorization model is implied |
| `redis.password_env` | Omitted | Optional nonempty environment variable name; when supplied, the variable must exist and contain a nonempty value |
| `log.level` | `info` | One of `debug`, `info`, `warn`, `error` |

Address validation checks syntax and port range, not DNS resolution or reachability. A syntactically valid unreachable Redis address produces an unready running service. The HTTP listener must successfully bind before startup is reported as successful.

Decode strictly: reject unknown fields, duplicate keys, extra YAML documents, invalid types, malformed addresses, invalid durations, and unsupported levels. Reject a missing config-path argument or unreadable file before serving HTTP.

Only the environment supplies passwords. Do not accept inline `password` or a Redis connection URL as alternate configuration. Omitting `password_env` supports the isolated, unauthenticated local Redis fixture; production credential and transport requirements are handled with deployment configuration in later work. The example is not a recommendation to expose Redis publicly.

Container verification explicitly overrides bind addresses and Redis hostnames through its supplied YAML file. The default loopback listener is not silently widened to all interfaces.

## 4. Startup, health, and error behavior

Startup order:

1. Parse flags and load/validate configuration, including required environment-secret resolution.
2. Construct the JSON stderr logger and the shared Redis client.
3. Configure and bind the HTTP listener.
4. Serve health endpoints and wait for termination or an unexpected server error.

Do not require a successful initial Redis connection before serving HTTP. Redis authentication, connectivity, or availability failures are readiness failures; malformed configuration is a startup failure.

### HTTP health contract

| Condition | `GET /healthz` | `GET /readyz` |
| --- | --- | --- |
| Serving, Redis reachable/authenticated | `200` | `200` |
| Redis unavailable, authentication rejected, or check deadline exceeded | `200` | `503` |
| Stopping while an existing connection is still served | `200` | `503` |
| Listener closed / process exited | No response | No response |

`/healthz` does not contact Redis. `/readyz` performs a Redis `PING` with a context bounded by both request cancellation and the configured readiness timeout. Connection establishment, pool waiting, and any client retries must fit within that total budget; a per-attempt timeout is insufficient. Configure the client accordingly and prove bounded failure through observable behavior.

The readiness handler checks stopping state before starting its dependency check and before reporting readiness. Stopping state is process-local lifecycle state, not correctness-critical gateway session/run state. It is not reset during shutdown.

Use small generic responses and safe structured error categories. Do not expose raw Redis errors, credentials, configuration dumps, or full environment contents in HTTP responses or logs. HTTP server read/write/idle handling must be bounded; exact transport timeout values are implementation-plan choices consistent with the readiness and shutdown budgets, not new product configuration knobs.

A successful `PING` establishes connectivity/authentication for that operation, not permissions for every future Redis command, writable topology, durability, or admission safety. Future admission remains responsible for failing closed on its own Redis errors.

Unexpected HTTP server failure exits nonzero after dependency cleanup. Normal server closure during graceful shutdown is not an error.

## 5. Logging

Use JSON `slog` output on stderr with the configured level. Record startup, shutdown, and safe failure categories sufficiently to diagnose this service. Do not create a parallel logging abstraction or promise the complete gateway audit lifecycle before those paths exist.

Configuration parsing and upstream client errors can contain user-supplied values. Report safe field/category information instead of blindly logging raw errors. Tests must exercise secret-bearing invalid input and upstream authentication failures, not only successful startup. Secret protection applies at every log level and to startup errors before the configured logger exists.

No full config dump, password, secret-bearing URL, or raw environment snapshot is permitted. The example contains only environment variable names, never secret values.

## 6. Graceful shutdown

On SIGINT or SIGTERM:

1. Mark the service as stopping before initiating HTTP shutdown.
2. Readiness checks that observe stopping return `503` even if Redis remains healthy.
3. Stop accepting new connections and drain active HTTP handlers within `server.shutdown_timeout`.
4. Close the Redis client after HTTP draining so active checks do not lose their dependency prematurely.
5. Exit successfully after normal cleanup. If draining fails or exceeds its deadline, force-close remaining HTTP connections, clean up the Redis client, log the safe failure category, and exit nonzero.

Do not keep the listener open for an artificial delay merely to expose the unready state. A health request after listener closure may receive a connection error; the contract does not promise an observable `503` interval to external polling.

This MR has no admitted work or running agents to drain. Do not introduce a fake admission switch, mark nonexistent work completed, or claim in-flight run recovery was verified. Lifecycle tests can use a controlled HTTP handler to exercise draining and deadline expiry without adding production endpoints.

## 7. Verification strategy

### Permanent regression checks

Keep tests that protect observable boundaries:

- Explicit config-path requirements, strict schema decoding, address/duration/level validation, and required environment-secret resolution.
- Startup and client-error handling cannot leak supplied secret sentinels.
- Liveness is independent of Redis.
- Readiness succeeds against real Redis, fails within its total deadline when unavailable, responds to request cancellation, and rejects readiness when stopping.
- Invalid startup and listener-bind failure exit nonzero.
- SIGTERM permits a clean exit; HTTP draining respects the configured deadline, with forced closure and nonzero exit on failure.

Use ordinary standard-library tests and deterministic boundary controls. Tests may inject a controlled health dependency or HTTP handler to establish race/timeout conditions, but real Redis must prove the actual client path. Do not pin incidental log wording, internal wiring, or copies of default values.

Fixtures own disposable Redis instances and their resources. They must never flush or modify an existing user database. No Discord, Hermes, LLM credentials, or model calls are required.

### Actual-process smoke scenario

Run an isolated real Redis and the actual compiled gateway:

1. Start the gateway while Redis is absent; observe live `200` and unready `503` without a process restart loop.
2. Start Redis and observe readiness recover to `200` without restarting the gateway.
3. Confirm both health endpoints return `200` while Redis is available.
4. Stop Redis; liveness remains `200`, readiness becomes `503` within its budget.
5. Restart Redis; readiness returns to `200` on the same gateway process.
6. Send SIGTERM and observe clean process exit and listener closure.
7. Capture service logs across these scenarios and verify supplied secret sentinels never appear.

Health during the short shutdown transition is covered deterministically rather than relying on a timing-sensitive external polling window. Exercise startup/bind errors and forced-drain failure as process/lifecycle regression checks.

### Runtime and final checks

During design exploration, `go` and `redis-server` were absent from PATH. Docker 29.7.2 and Docker Compose 5.5.0 CLIs were present; daemon access has not been verified. The approved verification approach uses containerized Go and Redis, avoiding host package installation. Confirm daemon access before execution and pin the supported toolchain/image versions in the implementation plan. A missing daemon or permission failure is an environment blocker, not a passing smoke test.

Containerized development/test commands are not production packaging. Keep transient scenarios disposable; remove throwaway scripts and resources after proof. README instructions must describe commands actually exercised, including explicit container bind/network configuration when used.

After integration, format changed Go files and run the full implementation test suite, race checks, `go vet`, build, and the actual-process smoke scenario. This document records intended verification only; none of those implementation checks have run yet.

## 8. Acceptance ownership and existing-plan corrections

| Owner | Required responsibility |
| --- | --- |
| T05, this MR | Runnable entry point, implemented-component configuration, safe structured logging, invalid-startup handling |
| T06, this MR | Redis-dependent readiness with live-but-unready startup, outage/recovery, bounded checks, HTTP lifecycle and shutdown |
| T08 | Identity configuration and its validation when identities are introduced |
| T14 | Capability/policy configuration and reference validation when policy is introduced |
| T15 | Real admission rejects Redis failures before model/backend work; readiness alone is not proof |
| T19 | In-flight run shutdown preserves recoverable metadata and does not invent completion |
| T23 | End-to-end correlated gateway lifecycle auditing |
| T24 | Production Dockerfile/Compose topology and Hermes packaging |

The existing milestone plan's early all-integrations configuration, `internal/audit` wrapper, and admission-rejection claim are superseded for T05/T06 by this scoped design. Update those descriptions alongside this spec so future execution does not reintroduce deferred work.

## 9. Completion and next gate

T05/T06 implementation is complete only when its included behavior, regression checks, actual-process smoke, and documentation agree, with exact commands/results recorded. An HTTP server that only compiles, a fake Redis fallback, or a readiness flag without the real client check is insufficient.

The design sections were approved in conversation. The next gate is user review of this written spec. After that approval, invoke `writing-plans` to produce the implementation plan for this MR. Do not write implementation code, push this branch, open an MR, or deploy as part of this documentation step.

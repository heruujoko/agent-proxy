# Agent Access Gateway Implementation Plan

**Execution reference:** Use the [assessed task breakdown](2026-09-11-gateway-tasks.md) for work-item dependencies, decision gates, and completion evidence. The eight tasks below remain the milestone-level outline.

**Goal:** Deliver the Discord-to-Hermes gateway defined by the [PRD](../../spec/agent-access-gateway-hermes-prd.md), with immutable input, deterministic authorization, Redis-backed coordination, progress delivery, and verified recovery behavior.

**Architecture:** One modular Go binary, one shared Redis, and one Hermes instance. External integration details stay behind platform/backend boundaries; correctness-critical state remains in Redis. See the [system design](2026-09-10-gateway-design.md).

**Tech Stack:** Go standard library for HTTP/JSON/logging/tests, Redis, a maintained Discord client, and a YAML parser. Exact dependency versions and Hermes distribution are not selected yet.

**Status:** Assessed on 2026-09-11; documentation only, not implementation approval. Architecture retained; integration and operational decisions remain open. Do not implement against invented Hermes endpoints. Task 1 gates Hermes-specific code, deployment manifests, and recovery assertions, not unrelated gateway work after implementation authorization. Proposed paths below are not existing implementation claims.

---

## Execution rules

- Implement vertical slices; add only packages used by that slice. No Telegram scaffolding or future backend implementations.
- Use Go's built-in test runner. Keep regression tests for observable behavior, boundaries, and failures, including PRD section 28; use actual service smoke scenarios for integration proof. Do not add tests that merely pin wiring or incidental defaults.
- Use real Redis for atomicity and recovery integration tests; deterministic HTTP fixtures for verifier/Hermes boundary failures. No real Discord or LLM credentials are needed for the automated suite.
- Run actual service smoke scenarios as well as tests. External credentials are prerequisites for live acceptance, not reasons to claim fixture tests prove live integration.
- Work on the current implementation branch; no isolated worktrees, commits, deployment, or publication without user authorization.
- Do not change PRD scope silently. Resolve contradictions in review before implementation; preserve unsupported-backend limitations explicitly.

## Task 1: Establish real integration contracts

**Files:** Update the integration-gate section of `docs/plans/2026-09-10-gateway-design.md` and this plan with verified facts and source references.

1. Obtain the selected Hermes source/release/deployment and pin the exact version.
2. Inspect its supported API and authentication; record actual session/run operations and event schemas.
3. Exercise a session, follow-up request, long-running execution, stream disconnect, status/final-output lookup, and duplicate create request where supported.
4. Record whether gateway death cancels execution, whether run creation is idempotent, and how concurrent sessions/runs behave.
5. Confirm Discord ingress mode, required intents/permissions, connection ownership, and multi-process strategy.
6. Resolve the design's proposed defaults and shared-Hermes trust assumptions. Pin dependencies only after these checks.
7. Select the verifier provider/model, structured-output contract, and authentication independently of Hermes. Separate source inspection from live probes; lack of live credentials blocks live acceptance, not an evidence-backed client implementation.

**Verify:** Save exact commands and redacted observations for the selected target. A capability matrix distinguishes observed support, unsupported behavior, and untested behavior. Do not mark durable recovery supported without demonstrating it. Track Hermes, Discord, verifier, and operational decisions separately using the breakdown's gates; gateway-independent work may proceed after implementation authorization without waiting for Hermes selection.

## Task 2: Create an operable Go service

**Files:** `go.mod`, `go.sum`, `cmd/gateway/main.go`, `internal/config/config.go`, `internal/config/config_test.go`, `internal/server/health.go`, `internal/server/health_test.go`, `config/config.example.yaml`, relevant lifecycle tests, and `README.md`. T05/T06 are one MR governed by the [service foundation design](../superpowers/specs/2026-09-11-service-foundation-design.md).

1. Initialize the module using the repository path and a supported Go version.
2. Require an explicit YAML config path and validate only implemented server, Redis, logging, and lifecycle settings. Reject unknown fields, missing configured environment secrets, and invalid values without printing secret values. Add integration/identity/capability settings and reference validation with their owning slices.
3. Compose dependencies in `main`; use `slog` directly and standard-library HTTP health endpoints, without a generic audit wrapper.
4. Start alive but unready during Redis failure; check readiness with a bounded per-request `PING`. On shutdown, mark stopping, drain HTTP within its deadline, then close Redis. No admission or in-flight run machinery is included yet.
5. Create only the Redis connection needed by the service; do not invent generic repository frameworks.

**Verify:** Configuration and HTTP/lifecycle checks cover invalid startup, secret-safe errors, listener-bind failure, bounded Redis readiness, graceful SIGTERM, and forced closure on drain failure. Run `go run ./cmd/gateway --config config/config.example.yaml` against isolated Redis and call `/healthz` and `/readyz`. Start without Redis and repeat outage/recovery: liveness stays healthy and readiness recovers without a gateway restart. Use containerized Go/Redis without host installation; verify daemon access before execution and document commands actually exercised. Real admission rejection on Redis errors belongs to T15; in-flight run shutdown proof belongs to T19.

## Task 3: Deliver the Discord admission slice

**Files:** `internal/platform/discord/adapter.go`, `internal/platform/discord/adapter_test.go`, `internal/request/envelope.go`, `internal/request/validation.go`, `internal/request/validation_test.go`, `internal/identity/resolver.go`, `internal/identity/resolver_test.go`, `internal/session/key.go`, `internal/session/key_test.go`.

1. Filter irrelevant/bot events and map trusted platform identifiers into the canonical envelope.
2. Preserve original text exactly, including Unicode, whitespace, and bot mentions; keep deterministic cleanup separate.
3. Resolve configured identity before expensive work; deny unknown or disabled actors.
4. Validate message length, attachment count/size/type, source context, and configured prohibited patterns.
5. Implement distinct DM/thread conversation keys and the approved shared-channel behavior.
6. Send a simple acknowledgement for this development slice; remove temporary echo behavior when Task 6 wires execution.

**Verify:** `go test ./internal/platform/discord ./internal/request ./internal/identity ./internal/session`. Cases must cover immutable input, malformed events, unsupported context, oversized attachments/prompts, and conversation isolation. Live smoke: a configured Discord user receives acknowledgement; an unknown user receives denial and causes no model call.

## Task 4: Add atomic admission coordination

**Files:** `internal/ratelimit/limiter.go`, `internal/ratelimit/limiter_integration_test.go`, `internal/run/store.go`, `internal/run/store_integration_test.go`, `internal/request/dedupe.go`, `internal/request/dedupe_integration_test.go`.

1. Choose and document an atomic Redis rate algorithm matching configurable burst/sustained semantics; enforce both limits in one atomic operation.
2. Create bounded duplicate-event ownership keyed by platform/source identity, with recoverable processing state rather than an irreversible seen flag.
3. Implement atomic per-user active-run check/reservation and exactly-once terminal release.
4. Implement conversation execution admission separately from user limits; reject busy conversations without adding a queue.
5. Check established active-run exhaustion before invoking the verifier; keep the atomic post-policy reservation to close races. A preflight check does not reserve capacity or guarantee later admission.
6. Persist run reservation/request ownership before external effects. Define retention for completed records; active runs must not disappear under ordinary TTL expiration. Persist quota decisions so duplicate processing does not repeatedly charge the same logical request.
7. Fail closed on Redis errors. Establish integration fixtures using isolated key namespaces and cleanup.

**Verify:** Run `REDIS_TEST_URL=redis://localhost:6379/15 go test ./internal/ratelimit ./internal/run ./internal/request -count=1`. Parallel admissions must not exceed limits; duplicate events create one logical request; duplicate finalization releases one reservation; worker lease expiry alone does not free an unknown active run. The test fixture must refuse unsafe cleanup of unrelated Redis keys.

## Task 5: Enforce verifier and deterministic policy

**Files:** `internal/verifier/classifier.go`, `internal/verifier/schema.go`, `internal/verifier/classifier_test.go`, `internal/policy/evaluator.go`, `internal/policy/evaluator_test.go`, `internal/request/pipeline.go`, `internal/request/pipeline_test.go`.

1. Add a bounded-time verifier HTTP client configured for the approved structured-output provider.
2. Validate fields, enums, confidence bounds, and requested capability names; preserve classification separately from input.
3. Implement approved fail-closed handling for `DENY`, `REVIEW`, unknown/low-confidence classifications, malformed responses, and timeout.
4. Evaluate requested capabilities against trusted role/group/source policy. No verifier result grants permissions.
5. Wire identity -> request rate checks and active-capacity preflight -> validation -> verifier -> policy -> definitive atomic user/conversation reservation. All denial paths terminate before session creation or Hermes invocation. Concurrent requests may pass preflight together, but only the definitive reservation authorizes a start.

**Verify:** `go test ./internal/verifier ./internal/policy ./internal/request`. Include verifier PASS plus policy DENY, unauthorized capability, missing permissions, malformed output, timeout, low confidence, unknown identity, exhausted quota, and immutable original input. Assert zero Hermes calls on every denied path using an observable boundary fixture.

## Task 6: Complete the first allowed execution

**Files:** `internal/agent/backend.go`, `internal/agent/hermes/client.go`, `internal/agent/hermes/events.go`, `internal/agent/hermes/client_test.go`, `internal/agent/hermes/events_test.go`, `internal/session/store.go`, `internal/session/store_integration_test.go`, `internal/run/manager.go`, `internal/run/events.go`, `internal/run/manager_test.go`, `internal/platform/discord/renderer.go`, `internal/platform/discord/renderer_test.go`, `cmd/gateway/main.go`.

**Prerequisite:** Task 1's verified wire contract. Adjust backend method signatures to that contract, not the conceptual PRD example.

1. Implement actual authentication, session creation/continuation, run creation, event subscription, and supported status retrieval with bounded network operations.
2. Normalize safe operational/tool/output/terminal events; exclude private reasoning and unsupported raw payloads.
3. Resolve session mappings with atomic creation ownership and the backend's verified idempotency/lookup semantics.
4. Persist run intent, start execution, save the external run ID, and detach lifecycle management from the ingress request timeout.
5. Store source/status-message identifiers, pending final output or a verified retrieval reference, and per-chunk delivery state. Throttle progress edits, honor rate-limit responses, handle long output, and suppress unintended mentions.
6. Finalize run accounting independently of rendering. Replace the development echo path completely.

**Verify:** `go test ./internal/agent/... ./internal/session ./internal/run ./internal/platform/discord`. Contract fixtures cover event translation, terminal errors, authentication errors, and session reuse. Live smoke: a permitted Discord request executes through Hermes, a follow-up reuses its session, and a task exceeding 30 seconds shows progress and delivers the final response.

## Task 7: Prove recovery and multi-process coordination

**Files:** `internal/run/recovery.go`, `internal/run/recovery_integration_test.go`, `internal/run/store.go`, `internal/platform/discord/renderer.go`, `tests/integration/gateway_test.go`.

1. Reconcile persisted reserved/starting/running/unknown records after startup or lease takeover.
2. Add ownership-aware transitions so stale workers cannot overwrite another worker's state.
3. Resume streams or query status/final output only where verified. Persist cursor/checkpoint information needed for supported recovery.
4. Retry uncertain creation only if backend idempotency or lookup proves it safe. Otherwise retain explicit unknown state and report the limitation.
5. Define operator reconciliation of unknown runs: require authoritative backend evidence before releasing capacity or retrying execution; never use age or lease expiry as proof of termination. Document the blocked-capacity limitation if evidence cannot be obtained.
6. Retry Discord delivery independently from persisted output/checkpoints; never restart successful execution to resend a message.
7. Test crash windows before external creation, after creation before Redis acknowledgement, during streaming, and after terminal state before delivery.
8. Run two gateway processes against one Redis; verify dedupe, session creation, conversation serialization, user concurrency accounting, and ownership takeover. Separately validate approved Discord ingress ownership.

**Verify:** `REDIS_TEST_URL=redis://localhost:6379/15 go test ./tests/integration ./internal/run -count=1` and `go test -race ./...`. Include a fixture run lasting more than 30 seconds, stream disconnect, process restart, duplicate terminal events, and Discord failure. Tests must assert the selected backend's actual recovery ceiling, not a stronger fixture-only guarantee. Repeat the recoverable live scenario against the pinned Hermes deployment.

## Task 8: Package and perform deployment acceptance

**Files:** `Dockerfile`, `docker-compose.yml`, `Makefile`, `config/config.example.yaml`, `README.md`; update the design and plan with verified limitations and operational decisions.

1. Build a minimal gateway image and configure the pinned Hermes deployment using its actual supported startup command.
2. Configure persistent Hermes volumes and Redis persistence, private networking, health checks, and environment-sourced secrets.
3. Document setup, configuration, limits, identities, recovery/unknown-run handling, log retention, Redis backup/restore expectations, and shutdown/upgrade behavior.
4. Add the real commands used for build, tests, and local service startup; no placeholder endpoints or fake readiness claims.
5. Run the complete acceptance matrix below before a trusted-cohort rollout. Remove temporary development-only paths after the end-to-end smoke succeeds.

**Verify:** `go test ./...`, `go test -race ./...`, `go vet ./...`, `go build ./cmd/gateway`, and `docker compose config --quiet`, followed by `docker compose up --build` and the live Discord flow. Verify no secrets appear in captured logs. Treat missing credentials or unsupported recovery as explicit acceptance blockers/limitations, not passes. Do not deploy or publish without separate user authorization.

## PRD acceptance and test coverage

| PRD requirement | Planned proof |
| --- | --- |
| Discord ingress, identity first, original input unchanged | Task 3 boundary tests and live acknowledgement; Task 5 pipeline call assertions |
| Burst/sustained limits before models; shared counters | Task 4 atomic Redis tests and Task 5 denied-path assertions |
| Concurrent-run abuse prevention | Task 4 reservations and Task 7 two-process stress/release cases |
| Structured classification; policy overrides PASS; no unauthorized capability | Task 5 verifier/schema/policy and boundary tests |
| Stable and isolated conversation mappings across restart | Tasks 3, 6, and 7 key/session/restart tests |
| Execution longer than 30 seconds; safe progress; final delivery | Task 6 live run and Task 7 timed integration fixture |
| Gateway restart and second instance preserve logical state | Task 7 real Redis/two-process recovery tests |
| Correlated lifecycle logs, no secret leakage | Tasks 2 and 8 captured-log checks across allow/deny/failure flows |
| Event translation and disconnect behavior | Tasks 6 and 7 verified Hermes fixtures and live recovery scenario |
| Unknown user, oversized prompt, malformed output, timeout | Tasks 3 and 5 explicit rejection tests |
| Deployment-ready operational behavior | Task 8 live startup, persistence, shutdown, and acceptance exercise |

## Review and implementation gates

This document is a dependency-ordered proposal, not a report of completed work. No build, tests, service smoke, or live Hermes checks have run for an implementation because implementation is outside this documentation MR.

Before execution, obtain implementation authorization. Resolve only the gates needed by the selected work item in the [task breakdown](2026-09-11-gateway-tasks.md); Hermes selection is not a dependency of pure gateway admission work. Replace unresolved protocol details with verified request/response contracts and reproduction commands before implementing those integrations. Do not weaken the PRD's observable acceptance criteria to accommodate an unsupported integration without user approval.

# Gateway Plan Assessment and Task Breakdown

**Date:** 2026-09-11
**Status:** Planning complete; implementation not started or authorized by this document.
**Sources:** [PRD](../../spec/agent-access-gateway-hermes-prd.md), [system design](2026-09-10-gateway-design.md), [milestone plan](2026-09-10-gateway-plan.md).

## Assessment

**Verdict:** Retain the modular Go service, shared Redis, one Hermes backend, immutable input, and deterministic authorization. The milestone sequence is useful, but integration assumptions and crash-boundary contracts must be resolved before the corresponding implementation can be accepted. The PRD's “Implementation Ready” label is not evidence that the selected Hermes supports the conceptual API.

This assessment covers the repository documents, not a running deployment. No Hermes distribution, verifier provider, Discord application, Go implementation, or dependency versions have been verified here.

| Finding | Consequence | Resolution / owner |
| --- | --- | --- |
| Milestone 1 combines unrelated Hermes, Discord, verifier, and product decisions | One unavailable backend can appear to block all development | Separate T01–T04; each gates only its consumers |
| Milestones 6 and 7 combine transport, sessions, execution, rendering, and recovery | Work items are too broad for independent completion evidence | Split T16–T22 with explicit prerequisites |
| Active-run reservation follows the verifier without a required preflight | Users already at capacity can still incur verifier cost, contrary to PRD §8 | T12/T15 add early capacity rejection and retain atomic post-policy reservation |
| Dedupe does not specify persisted admission progress | Lease takeover can charge a request again or repeat effects | T10/T11 persist stage and quota outcome; terminal dedupe retention is explicit |
| Delivery state does not explicitly retain final output | A crash after completion can leave a reply impossible to reconstruct | T19/T20 persist output or a verified durable retrieval reference and chunk checkpoints |
| Unknown-run capacity is retained without an operational resolution contract | Safe accounting can leave a user/conversation indefinitely busy | T21/T25 require evidence-backed reconciliation; no blind retry or age-based release |
| Audit requirements have broad coverage but no end-to-end owner | Correlation and redaction can be deferred or missed between modules | T23 owns full allow/deny/failure trace evidence |
| Source-backed contract checks and live verification are conflated | Fixtures can accidentally be presented as proof of live capabilities | T01–T03 distinguish source evidence from live probes; T26 owns live acceptance |

The design and milestone plan now reflect these corrections. Defaults below remain recommendations, not recorded user approval. No Telegram implementation, queue, sidecar, admin UI, tool authorization, or multiple-Hermes work is added.

## Gates and tracking rules

All T01–T26 items start **pending**. A dependency is a completion prerequisite, not a claim that the prerequisite has run. Mark an item **blocked** only when its required evidence/input is unavailable, recording exactly what is missing. Do not mark unsupported behavior as passed.

| Gate | Evidence / decision required | Blocks |
| --- | --- | --- |
| Implementation authorization | User authorizes moving beyond documentation | Code changes and implementation experiments |
| Hermes contract, T01 | Exact target/revision, supported operations, source references, capability matrix, available live observations | T16–T19 backend execution, Hermes packaging, backend recovery claims |
| Discord contract, T02 | Client/API version, ingress ownership, intents/permissions, supported message contexts | T07 ingress and T20 rendering integration |
| Verifier contract, T03 | Provider/model, authentication, structured output and error/timeout contract | T13 verifier client |
| Behavioral decisions, T04 | Fail-closed policy, conversation defaults, quota semantics, retention rules | Dependent admission/policy/state behavior |
| Operational readiness, T25 | Capacity, access, recovery objective, retention values, backup/restore and unknown-run procedures | Shared deployment acceptance |
| Live environment, T26 | Authorized test Discord context and selected backend/verifier credentials | Live acceptance only; secrets never go in documents |

T01–T03 can finish source-backed contract discovery while clearly listing untested live behaviors. Code may rely on documented operations with fixtures; recovery support remains unverified until its live scenario passes. An unavailable Hermes target blocks Hermes-specific work, not configuration, identity, pure policy, Redis coordination, or contract discovery for the other integrations. Never choose a convenient but unverified endpoint to clear a gate.

### Shared contracts before concurrent implementation

- **Input:** PRD §6 envelope; immutable `original_text`; trusted actor/source IDs; optional deterministic cleanup stays separate. Classifier metadata cannot replace input or grant capabilities.
- **Admission:** trusted platform event -> recoverable duplicate ownership -> identity -> atomic request budgets and active-capacity preflight -> validation -> verifier -> policy -> atomic user/conversation reservation -> session -> run. Apply cheap platform filtering before allocating dedupe state. A preflight success is not a reservation; races can still cause a later busy response.
- **State:** request stage and quota decision; conversation mapping; run intent/external identity; owner token/lease; execution state; independent delivery state. T10 owns duplicate admission, T12 owns run/accounting transitions, T19 integrates execution, and T20 owns delivery checkpoints.
- **Accounting:** request quotas are consumed once per admitted logical request within the recorded dedupe window. Policy/validation denial does not refund an already-consumed request budget. User and conversation capacity are acquired together only after ALLOW and released once on a known terminal outcome. These quota semantics are proposed for T04 review.
- **Execution:** `reserved -> starting -> running -> completed | failed`; ambiguous external start becomes `unknown`. T12 must define legal transitions, including definitive failure before `running` and evidence-backed reconciliation from `unknown`. Lease expiry changes ownership, not execution outcome.
- **Events and delivery:** backend-specific payloads become allowlisted internal operational/output/terminal events. No raw reasoning or arbitrary tool payload forwarding. Execution finalization and durable pending delivery must not leave a crash gap. Retain undelivered output/checkpoints independently of completed-run retention.
- **External effects:** Redis atomicity is not exactly-once Hermes creation or Discord sending. Retry uncertain mutations only with verified idempotency or authoritative reconciliation. Document residual duplicate/orphan risks.
- **Ownership:** one integration owner edits `cmd/gateway/main.go`, shared contracts, and configuration composition. Concurrent workers edit disjoint files or serialize shared mutations; they skip formatters, builds, lint, and tests until integration is ready for one validation pass.

## Work items

Paths below are proposed implementation locations, not files asserted to exist. Each task delivers working behavior and its stated proof, not empty interfaces or placeholder packages. Evidence records use exact commands, versions, redacted observations, and remaining limitations. Keep the PRD §28 regression checks; do not add tests of incidental wiring/defaults. Live smoke scenarios supplement, never replace, deterministic boundary checks.

### Integration decisions — milestone 1

#### T01 — Verify the selected Hermes contract

- **Depends on:** selected source/release or deployment being available; no other task.
- **Scope:** Record target/revision/authentication and actual session, run, event, status, final-output, idempotency, lookup, concurrency, and disconnect semantics in the design's integration gate. Separate source-supported, live-observed, unsupported, and untested capabilities. Do not implement the adapter here.
- **Done when:** Every PRD §29 question has a source-backed answer or an explicit missing prerequisite. Available live probes cover session follow-up, >30-second execution, stream interruption, duplicate creation, and gateway/client death. Missing live probes remain T26 blockers for those claims. If durable runs are absent, document the supported transport and recovery ceiling; scope changes require user approval.

#### T02 — Verify Discord ingress and delivery contracts

- **Depends on:** no other task; test bot credentials only for live probes.
- **Scope:** Select maintained client/version and event transport. Record bot-message filtering, DM/thread identifier semantics, intents including message-content access, permissions, output constraints, rate-limit handling, and single-/multi-process connection ownership. Do not assume an HTTP load balancer scales bot event connections.
- **Done when:** Source references and an ingress-ownership decision are recorded; available probes establish text/event access and reply/edit permission in supported contexts. Identify required test-channel permissions and untested live cases for T26.

#### T03 — Verify the intent-verifier contract

- **Depends on:** no other task; selected provider/model must be available for contract discovery.
- **Scope:** Record API/version, environment-sourced authentication, structured-output request/response shape, refusal/error semantics, timeout and response-size boundaries. Preserve the PRD classification fields; select no permissions based on provider output.
- **Done when:** Documented wire examples support deterministic fixtures for valid output, refusal, malformed output, timeout, and authentication errors. Record live sample classification when credentials are available; otherwise live verification stays open in T26.

#### T04 — Record behavioral and state decisions

- **Depends on:** no other task; obtain user decisions only where tradeoffs require them.
- **Scope:** Recommend fail-closed DENY/REVIEW/unknown/low-confidence handling; per-user DMs, shared threads, explicitly threaded shared channels; conversation busy rejection without queueing; atomic burst/sustained algorithm and quota charging semantics. Define prompt/attachment size units, capability mappings and policy precedence, duplicate window, retention classes, and protected in-flight/pending-delivery records. Distinguish ingress authorization from shared-Hermes tool/memory safety.
- **Done when:** Each decision is accepted or explicitly open with its affected tasks blocked. Numeric operational retention/capacity values may remain T25 rollout gates, but code-facing semantics cannot be ambiguous. No “review” approval workflow or downstream tool enforcement is implied.

### Operable service — milestone 2

T05 and T06 are combined in one MR. Their approved scope and acceptance boundaries are defined in the [service foundation design](../superpowers/specs/2026-09-11-service-foundation-design.md); implementation remains pending.

#### T05 — Bootstrap service and validated configuration

- **Depends on:** implementation authorization; no Hermes contract dependency.
- **Scope:** `go.mod`, `go.sum`, `cmd/gateway/main.go`, `internal/config/config.go`, `config/config.example.yaml`. Select supported Go/Redis/YAML versions; compose only implemented server/Redis/logging components. Require an explicit config file, reject unknown fields and invalid values, and resolve environment-sourced secrets without disclosure. Use `slog` directly rather than creating an audit wrapper. Integration-specific settings arrive with their owning slices; identity/capability reference validation belongs to T08/T14.
- **Done when:** Configuration regression checks pass; the actual service starts with valid base configuration, exits clearly for invalid configuration, and emits structured logs without printing secret values. No enabled integration uses a fake backend fallback.

#### T06 — Add Redis readiness and graceful shutdown

- **Depends on:** T05.
- **Scope:** `internal/server/health.go`, Redis client composition, lifecycle in `cmd/gateway/main.go`. Start alive but unready when Redis is unavailable. Keep liveness independent of Redis; check readiness with a bounded per-request Redis `PING`. Mark stopping before HTTP draining, then close dependency clients. Establish disposable real-Redis fixtures, never flushing unrelated keys. No admission or in-flight run machinery exists in this slice.
- **Done when:** Run the actual service, including startup without Redis; verify healthy liveness and failed readiness, then readiness recovery without gateway restart. Repeat Redis outage/recovery and verify bounded checks and graceful SIGTERM exit. Exercise invalid startup, listener-bind failure, and forced shutdown on drain deadline. T15 owns real admission rejection on Redis failure; T19 owns in-flight run shutdown proof.

### Discord admission — milestone 3

#### T07 — Receive immutable canonical Discord requests

- **Depends on:** T02, T05.
- **Scope:** `internal/platform/discord/adapter.go`, `internal/request/envelope.go`. Filter irrelevant/bot events and map trusted message/source IDs, raw text, attachments, and reception time. Define supported malformed/missing-field behavior without normalizing original text.
- **Done when:** Boundary tests preserve Unicode, whitespace, and bot mentions exactly; unsupported events cannot enter the pipeline. Available live test-channel events produce canonical input without logging full prompts. Any development acknowledgement is explicitly non-executing and replaced by T20.

#### T08 — Resolve identity and validate deterministic limits

- **Depends on:** T04, T05; canonical envelope contract above.
- **Scope:** `internal/identity/resolver.go`, `internal/request/validation.go`. Introduce and validate identity configuration with this slice. Resolve static trusted identities, reject unknown/disabled users, enforce configured text/attachment/context/pattern checks. Do not fetch arbitrary attachment URLs. Validation follows request budgets in the integrated pipeline.
- **Done when:** Known/unknown/disabled identity, malformed request, boundary-sized text, attachment limits, and unsupported context have deterministic behavior. User text cannot inject roles or source IDs. T15 proves rejection occurs before model/backend calls.

#### T09 — Define conversation isolation keys

- **Depends on:** T04, T07.
- **Scope:** `internal/session/key.go`. Build keys from verified Discord identifiers for per-user DMs and shared threads; implement the selected shared-channel restriction.
- **Done when:** Follow-ups within a conversation resolve to the same key; distinct DM users/guilds/channels/threads cannot accidentally collide. Shared-thread context sharing is intentional and documented, not represented as private per-user memory.

### Atomic shared coordination — milestone 4

#### T10 — Persist recoverable duplicate admission

- **Depends on:** T04, T06, T07, T08.
- **Scope:** `internal/request/dedupe.go`. Atomically claim platform/source message identity; persist logical request ID, admission stage, owner token, and outcome. Filter untrusted/irrelevant events before persistent allocation, bound terminal retention, protect in-flight ownership, and define safe takeover without an irreversible seen-only flag.
- **Done when:** Concurrent duplicate events yield one logical request; worker loss permits recovery without redoing known-completed effects. Terminal expiry follows the documented dedupe window; in-flight requests do not disappear under it. Use isolated real Redis.

#### T11 — Enforce atomic burst and sustained budgets

- **Depends on:** T04, T06, T10.
- **Scope:** `internal/ratelimit/limiter.go`, admission quota persistence. Enforce both budgets in one atomic operation using the chosen time/refill semantics; store quota outcome with the request stage so takeover cannot charge twice. Return a useful limit response and fail closed on Redis errors.
- **Done when:** Real Redis checks cover burst/sustained exhaustion, refill/window boundary, cross-worker races, and duplicate takeover between quota debit and acknowledgement. No partial budget debit occurs on an operation that rejects admission under the chosen semantics.

#### T12 — Reserve run capacity and define legal transitions

- **Depends on:** T04, T06, T09, T10.
- **Scope:** `internal/run/store.go`. Add user/conversation preflight, atomic combined reservation, persisted run intent, ownership tokens/leases, allowed state transitions, and idempotent terminal release. Keep unknown/in-flight runs charged and discoverable after lease expiry. Persist request-to-run association with reservation.
- **Done when:** Real Redis races cannot exceed user or conversation capacity or leak one reservation when the other fails. Duplicate terminal events release once; stale workers cannot mutate current state; rejected/pre-run failures release safely; unknown and lease-expired work stays charged. Discovery supports recovery without relying on process-local lists.

### Authorized admission — milestone 5

#### T13 — Implement bounded structured classification

- **Depends on:** T03, T05; metadata contract above.
- **Scope:** `internal/verifier/classifier.go`, `internal/verifier/schema.go`. Implement real provider transport with bounded request time/response size; validate required fields, enums, capabilities, confidence, and structured parse errors. Keep original input separate from derived metadata.
- **Done when:** Contract fixtures cover valid metadata, malformed/missing fields, refusal, timeout, and unauthorized/unknown capability names. Errors cannot become PASS or bypass classification. A live sample is required in T26, not inferred from fixtures.

#### T14 — Implement deterministic capability policy

- **Depends on:** T04, T13.
- **Scope:** `internal/policy/evaluator.go`. Introduce capability/policy configuration and validate its references with this slice. Evaluate trusted actor/role/group/source constraints and validated classification; deny missing permission mappings, DENY/REVIEW, and configured unsafe/uncertain classifications. Return decision, permitted/denied capabilities, and reason codes without building an approval queue.
- **Done when:** Verifier PASS plus insufficient permission returns DENY; confidence cannot grant capability. Conflicting rules obey the recorded precedence; unknown/low-confidence input fails closed. Original prompt is unchanged.

#### T15 — Integrate the deny-before-Hermes pipeline

- **Depends on:** T08, T09, T10, T11, T12, T13, T14.
- **Scope:** `internal/request/pipeline.go`, composition in `cmd/gateway/main.go`. Enforce the shared admission sequence, propagate correlation IDs, and preserve denial/audit outcomes. Established active-capacity exhaustion rejects before the verifier; final atomic reservation after policy closes races.
- **Done when:** Boundary fixtures observe zero verifier/Hermes calls for unknown users, exhausted quotas/capacity, and invalid input; zero Hermes/session-create calls for every classifier/policy denial. Concurrent preflight successes cannot start beyond capacity. An authorized request carries byte-preserved original text to the execution boundary, with no fake production execution path.
- **Redis outage proof:** Exercise the actual admission path with unavailable/failing Redis and observe rejection before verifier/Hermes work. A successful readiness check does not authorize admission; operation errors must still fail closed. This is the admission proof deferred from T06.

### First complete execution — milestone 6

#### T16 — Implement the verified Hermes transport

- **Depends on:** T01, T05.
- **Scope:** `internal/agent/backend.go`, `internal/agent/hermes/client.go`. Define the backend seam from actual operations, authentication, session continuation, run creation, subscription, and supported status/final retrieval. Bound network operations without imposing one short total timeout on long-running execution.
- **Done when:** Source-derived contract fixtures cover success, authentication failure, definite rejection, and ambiguous timeout. Mutating retries obey verified safety semantics. Unsupported operations are not simulated as success; available live probes match the selected version.

#### T17 — Normalize safe backend events

- **Depends on:** T16.
- **Scope:** `internal/agent/hermes/events.go`, `internal/run/events.go`. Translate actual transport frames into internal operational/tool/output/terminal events. Bound frame handling, exclude private reasoning and arbitrary raw payloads, and preserve meaningful event IDs only where supported.
- **Done when:** Fixtures prove correct translation, terminal failure, safe treatment of malformed/unknown frames, and non-forwarding of internal-only content. No consumer depends on Hermes event names. Disconnect is not interpreted as successful completion.

#### T18 — Resolve persistent Hermes sessions atomically

- **Depends on:** T09, T12, T16.
- **Scope:** `internal/session/store.go`. Persist conversation mappings and guard external session creation; use verified idempotency/lookup where available. Keep Redis as mapping state, not a second Hermes conversation history.
- **Done when:** First request and follow-up share the intended session; isolated conversations do not. Concurrent creation and gateway restart retain one authoritative mapping. Crash-after-create behavior matches verified backend support; orphan-session risk is explicit when reconciliation is unavailable.

#### T19 — Orchestrate persisted asynchronous runs

- **Depends on:** T15, T17, T18.
- **Scope:** `internal/run/manager.go`, run-store integration. Persist intent before external creation, then external identity; detach execution lifecycle from ingress timeout. Distinguish definite start failure from unknown outcome. Persist terminal outcome and reconstructible pending delivery before releasing accounting; renderer failure must not undo execution success.
- **Done when:** A fixture run >30 seconds outlives initial handling and finishes with one terminal release. Failures before/after external creation leave the correct state; terminal persistence cannot leave completed work without final-output recovery data. Graceful shutdown retains recoverable metadata rather than inventing completion.

#### T20 — Deliver durable Discord progress and final replies

- **Depends on:** T02, T17, T19.
- **Scope:** `internal/platform/discord/renderer.go`, delivery-state integration. Send prompt acknowledgement, coalesce status edits, honor platform retry limits, disable unintended mentions, and split final output within actual constraints. Persist status ID, final output/retrieval reference, and per-chunk delivery checkpoints; keep undelivered output under its own retention policy. Replace development acknowledgement/echo behavior completely.
- **Done when:** Boundary fixtures cover rate limits, long output, mention suppression, and delivery failure independent of run completion. Available live smoke shows safe progress, final reply, session follow-up, and >30-second execution. Unknown send outcomes disclose residual duplicate risk instead of promising exactly-once messages.

### Recovery and hardening — milestone 7

#### T21 — Recover execution and delivery after process loss

- **Depends on:** T19, T20.
- **Scope:** `internal/run/recovery.go` and ownership-aware store/renderer integration. Discover unfinished execution and pending delivery, take over expired owners, resume/query only when supported, and reconcile unknown starts with authoritative evidence. Never blindly restart work to recover a reply; never expire unknown capacity based on age.
- **Done when:** Crash checks cover before create, after create before Redis acknowledgement, during streaming, and after terminal persistence before delivery acknowledgement. Stale owners cannot overwrite takeover. Pending chunks recover from retained data; unknown outcomes remain explicit if the backend cannot resolve them. Live recovery remains bounded by T01 capabilities.

#### T22 — Prove coordination across two real gateway processes

- **Depends on:** T21.
- **Scope:** `tests/integration/gateway_test.go` and relevant Redis integration fixtures. Launch two gateway processes against isolated real Redis with deterministic external boundaries, not merely two goroutines. Exercise duplicate admission, quota accounting, session mapping, user/conversation capacity, stream disconnect, worker death, and duplicate terminal events.
- **Done when:** No process-local affinity is required for logical state; takeover preserves mapping and accounting and does not recreate known execution. Run race checks after integration. Validate Discord connection ownership separately against T02; worker tests do not prove two live bot connections are safe.

#### T23 — Prove correlated and secret-safe lifecycle auditing

- **Depends on:** T15, T20, T21.
- **Scope:** `internal/audit/logger.go` and lifecycle callsites. Carry one request ID through identity, rate/validation, intent/policy, session/run, recovery, and platform delivery. Record PRD §19 fields when available; distinguish unavailable identifiers on early denials from broken propagation. Do not log full prompts by default, secret-bearing URLs, headers, or raw upstream errors containing secrets.
- **Done when:** Capture actual service logs for allowed, denied, failed, recovered, and delivery-failed scenarios. Reconstruct each lifecycle by correlation ID and verify supplied secret sentinels never appear. Redis request records and logs follow separate access/retention rules; neither is falsely presented as a durable audit archive.

### Deployment acceptance — milestone 8

#### T24 — Package the verified single-host topology

- **Depends on:** T01, T02, T03, T06, T20.
- **Scope:** `Dockerfile`, `docker-compose.yml`, `Makefile`, `config/config.example.yaml`. Package one gateway, persistent Redis, and the pinned Hermes with its real startup command and volumes; private networking, health checks, environment-sourced secrets, reproducible build/test/start commands. No public publication or production deployment without authorization.
- **Done when:** Build and `docker compose config --quiet` pass. Authorized local `docker compose up --build` reaches actual readiness using the intended images/configuration; readiness is not inferred from container creation. Dependency failure prevents admission without killing liveness.

#### T25 — Complete operational and trust readiness

- **Depends on:** T04, T21, T23, T24.
- **Scope:** `README.md`, configuration examples, design/plan updates. Record approved cohort and shared-Hermes trust assumptions, workload/backend capacity, least-privilege credentials, concrete request/output/log retention, Redis persistence/backup/recovery objective, unknown-run reconciliation, delivery exhaustion, shutdown, and upgrade procedures. Use a documented operator procedure, not a new admin service.
- **Done when:** An operator can deploy the configured topology and diagnose busy/unknown runs without blindly clearing state. A disposable persistence/backup-restore exercise demonstrates the chosen recovery objective and preserves mappings/accounting consistently. If authoritative unknown-run evidence is unavailable, document indefinite capacity retention and block rollout until that limitation is explicitly accepted or resolved.

#### T26 — Execute the complete acceptance matrix

- **Depends on:** T22, T23, T25; authorized live environment and credentials.
- **Scope:** Run the PRD §26/§28 matrix below and the milestone plan's build/test/race/vet/Compose commands once against the integrated implementation. Exercise real Discord -> verifier -> policy -> Hermes -> Discord, a follow-up, >30-second work, denial paths, restart/disconnect behavior, and delivery failure. Record exact versions, commands, observed results, and unsupported/untested limitations.
- **Done when:** Every applicable row has evidence; missing credentials, failed scenarios, or unmet PRD criteria are blockers, not passes. Source-backed backend recovery limitations remain explicit; any reduction of required acceptance needs user approval. After the end-to-end smoke succeeds, remove temporary scripts/development-only paths, update existing docs and any existing changelog, and obtain separate authorization for shared rollout/publication.

## Scheduling and milestone exits

Prefer the first working admission/execution path over building all abstractions in advance. This is a scheduling proposal, not a request to start agents or implementation now.

| Milestone | Work items | Exit |
| --- | --- | --- |
| 1 — Contracts | T01, T02, T03, T04 | Independent evidence/decision records; unresolved gates named |
| 2 — Service | T05, T06 | Runnable service with real Redis health behavior |
| 3 — Admission | T07, T08, T09 | Immutable, identified, validated, conversation-scoped requests |
| 4 — Coordination | T10, T11, T12 | Atomic budgets, dedupe, and capacity under contention |
| 5 — Authorization | T13, T14, T15 | Denied input demonstrably cannot reach Hermes |
| 6 — Execution | T16, T17, T18, T19, T20 | First live long-running Discord round trip and session follow-up |
| 7 — Recovery | T21, T22, T23 | Proven crash boundaries, two-process state, correlated logs |
| 8 — Deployment | T24, T25, T26 | Operable topology and complete recorded acceptance |

After authorization, T01–T04 discovery and T05 base service work can proceed independently. After T05, T06, T07, T08, T13, and T16 can progress when their individual gates clear. After T10, T11 and T12 have separate implementations; freeze their shared admission contract and serialize shared composition edits. T16/T17 may progress alongside authorization. T19 is the execution integration join; T20 is the first complete user-facing slice. After T21, T22 and T23 can be verified while T24 packaging progresses, with one owner for shared files. Do not treat concurrent planning as permission to run validation against half-integrated work.

## PRD acceptance coverage

Each row requires recorded observations, not just a linked test filename. “Live” means the selected actual integration; fixture-only evidence must be labeled.

| PRD §26 criterion | Owner tasks | Required proof |
| --- | --- | --- |
| Discord user sends a message | T07, T26 | Live supported-context ingress |
| Original message stored/passed without semantic rewriting | T07, T15, T19 | Exact original text at admission and backend boundary |
| Identity resolved before model execution | T08, T15 | Unknown/disabled actor invokes no model |
| Burst/sustained limits reject before verifier/Hermes | T11, T15 | Boundary/race cases and zero model calls |
| Concurrent limit prevents another run | T12, T15, T22 | Preflight rejection plus atomic race protection |
| Counters stored in Redis | T11, T12, T22 | Shared-worker/restart behavior with real Redis |
| Allowed prompt has structured metadata | T13, T26 | Parsed fixture and live classification |
| Denied prompt never reaches Hermes | T14, T15 | All rejection branches exercise backend boundary |
| Policy can deny verifier PASS | T14, T15 | PASS plus missing permission rejects |
| Verifier cannot grant unauthorized capability | T13, T14, T15 | Untrusted capability/confidence never authorizes |
| First message resolves/creates a session | T18, T26 | Live first-request mapping |
| Follow-up reuses the session | T18, T26 | Live follow-up identity |
| Configured boundaries do not share sessions accidentally | T09, T18 | Distinct DM/thread/context cases |
| Restart preserves session mapping | T18, T21, T26 | Reload and live follow-up after restart |
| Hermes task >30 seconds completes | T19, T20, T26 | Timed fixture and live completion |
| Initial handling is not one short blocking HTTP response | T19, T26 | Acknowledgement precedes long-run completion |
| Progress is rendered during execution | T17, T20, T26 | Safe live intermediate status |
| Final output delivered on completion | T20, T21, T26 | Live reply and crash/pending-delivery recovery |
| Restart retains Redis-backed logical state | T21, T22, T25 | Run/source IDs, ownership, quotas, dedupe, mappings survive |
| Second instance requires no process-local session state | T22 | Two actual processes using one Redis |
| Full lifecycle traced by correlation ID | T23, T26 | Captured allow/deny/failure/recovery traces |
| Secrets not logged | T05, T23, T26 | Config/upstream error and live log redaction checks |

## PRD required-test coverage

The PRD explicitly requests these regression contracts. Keep deterministic fixtures credential-free; use isolated real Redis for its atomicity/persistence behavior.

| PRD §28 test | Owner tasks |
| --- | --- |
| Request envelope preserves original input | T07, T15 |
| Deterministic validation | T08 |
| Identity resolution | T08 |
| Rate-limit decisions | T11, T15 |
| Intent schema parsing | T13 |
| Policy decisions | T14 |
| Conversation-key generation | T09 |
| Hermes event translation | T17 |
| Redis rate limiting | T11 |
| Session mapping | T18 |
| Duplicate message handling | T10, T11, T22 |
| Active-run accounting | T12, T19, T22 |
| Mocked Hermes run >30 seconds | T19, T22 |
| Event stream disconnect | T17, T21, T22 |
| Gateway restart | T18, T21, T22 |
| Two gateway processes sharing Redis | T22 |
| Verifier PASS + policy DENY | T14, T15 |
| Unknown user | T08, T15 |
| Malformed verifier output | T13, T15 |
| Verifier timeout | T13, T15 |
| Oversized prompt | T08, T15 |
| Unauthorized capability request | T14, T15 |
| Concurrent-run abuse | T12, T15, T22 |

## Immediate next work

Continue with T01–T04 contract discovery and decision recording when implementation investigation is authorized; T05/T06 are the first backend-independent coding slice after code authorization. Do not start T16 against the PRD's illustrative endpoints. This assessment does not claim live verification, implementation completion, or deployment readiness.

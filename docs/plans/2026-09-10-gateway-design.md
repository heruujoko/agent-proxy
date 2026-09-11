# Agent Access Gateway — System Design

**Status:** Proposed for review; not an implementation approval.
**Source:** [Gateway PRD](../../spec/agent-access-gateway-hermes-prd.md)
**Implementation sequence:** [Build plan](2026-09-10-gateway-plan.md)

## Scope and success criteria

Build one stateless Go service between Discord and one shared Hermes instance, using Redis for correctness-critical gateway state. A known user can submit an unchanged prompt, pass identity/rate/validation/verifier/policy checks, reuse a conversation session, observe a run longer than 30 seconds, and receive its final output. Restarting the gateway preserves session and run metadata. Two gateway processes can coordinate against the same Redis without process-local session ownership.

Telegram activation, human approval, tool/MCP enforcement, enterprise IAM, OPA, a distributed queue, an admin UI, Postgres history, and multiple Hermes instances remain outside V1. This is ingress authorization, not a guarantee of downstream tool safety or cross-user Hermes memory isolation.

## Architecture decision

Use a modular Go binary, Redis, and one Hermes deployment. Prefer Go standard-library HTTP, JSON, logging, and testing facilities; select maintained Discord, Redis, and YAML clients during implementation. No framework or service mesh is required.

Alternatives considered:

- Separate ingress and execution services allow independent scaling but require a durable handoff and more operational components. Defer until demonstrated need.
- Implementing controls inside Hermes offers runtime access but couples upgrades and contradicts the PRD's no-internals-change boundary. Reject for V1.

```text
Discord event
  -> canonical envelope / duplicate ownership
  -> identity -> request rate limits -> deterministic validation
  -> structured verifier -> deterministic policy
  -> atomic active-run admission -> session resolution
  -> Hermes run -> normalized events -> Discord renderer

Redis: request ownership, limits, sessions, run state, leases, delivery state
Hermes: agent loop, tools, skills, memory, local execution state
```

The pipeline is sequential: no policy or model call precedes identity resolution. Early duplicate detection must not allocate unbounded state for unauthenticated traffic; authenticate the platform connection and reject irrelevant/bot events first.

## Module boundaries

| Module | Responsibility |
| --- | --- |
| `platform/discord` | Receive platform events; construct envelopes; acknowledge and render replies |
| `request`, `identity` | Preserve original input; resolve trusted config-backed identity; validate deterministic limits |
| `ratelimit` | Atomically enforce configurable burst and sustained budgets in Redis |
| `verifier`, `policy` | Produce validated classification metadata; independently authorize capabilities |
| `session`, `run` | Resolve conversations, reserve capacity, manage execution lifecycle and recovery |
| `agent/hermes` | Encapsulate the verified Hermes wire protocol and normalize events |
| `audit` | Correlated structured lifecycle logging without secrets |

Implement these boundaries as needed by vertical slices, not empty packages. The PRD's backend abstraction is justified at the external integration seam; do not create speculative alternate backends.

## Contracts and state

### Request and authorization

The canonical envelope follows PRD section 6: request ID, resolved actor, platform source IDs, immutable `original_text`, optional deterministic `clean_text`, attachments, and reception time. Identity and roles come from trusted configuration, never user text. Derived verifier metadata and execution context remain separate from original input.

Validate message and attachment limits before model invocation. Do not fetch arbitrary attachment URLs as an incidental implementation detail; any fetching needs an explicit trusted-source and size-bounded policy.

Parse verifier output strictly against the PRD schema and validate enums, required fields, capability names, and confidence bounds. Proposed V1 behavior is fail-closed on timeout, malformed output, `DENY`, `REVIEW`, unknown intent, or insufficient confidence. `REVIEW` returns a non-executing response; there is no approval queue.

Policy evaluates actor permissions, source context, and requested capabilities. A verifier `PASS` or confidence value cannot grant permissions. Missing capability mappings deny. Pass the original prompt to Hermes without semantic rewriting; authorization metadata must not masquerade as user text.

### Conversations

Use per-user DM sessions and shared-thread sessions following PRD section 12. Proposed default: require explicit threading in shared channels. Conversation keys include platform and relevant guild/channel/thread/user identifiers. Shared threads are intentionally shared contexts, not privacy boundaries.

Store conversation-to-Hermes mappings in Redis. Guard session creation atomically. A lock alone cannot prevent duplicate external sessions after a crash; use Hermes idempotency or lookup if supported and document any remaining orphan-session possibility.

Serialize active execution within a conversation until Hermes concurrency semantics are verified. Return a clear busy response rather than introducing a queue. Per-user active-run limits remain independently configurable.

### Runs and delivery

Proposed internal execution states: `reserved -> starting -> running -> completed | failed`. An ambiguous external start is `unknown`, not automatically `failed`; it retains capacity until reconciliation establishes an outcome. Delivery has separate pending/sent/failed state.

Persist run intent, actor ownership, source identifiers, and reservation before the external create operation. Persist the external run ID immediately after success. Use a stable request/run identity as an idempotency key only if Hermes supports that contract.

Atomically check and reserve active-run capacity; repeat the definitive check after policy even if an early limit check avoids verifier cost. Terminal transitions release capacity once. Do not expire active records or free capacity merely because a worker lease expired. Use ownership leases and compare-and-set transitions so stale workers cannot finalize another worker's work.

Record status-message IDs and recoverable rendering state in Redis. Track event cursors only if the backend supports meaningful replay. Execution success survives a Discord delivery failure. Reconnection, retries, and duplicate terminal events must not recreate execution or double-release capacity.

Exactly-once cross-system effects are not promised. A crash after an external operation but before its acknowledgement requires backend idempotency/reconciliation; Discord delivery can also be ambiguous. Document residual duplicate-message risk rather than claiming Redis eliminates it.

### Hermes integration gate

The repository PRD does not pin a Hermes distribution/version or establish real endpoint contracts. Before finalizing the adapter, record and exercise:

1. Exact distribution, revision/image, and authentication.
2. Persistent session creation and continuation semantics.
3. Durable run creation, idempotency, and lookup by request identity.
4. SSE/WebSocket/event protocol, event IDs, ordering, replay, and safe payload fields.
5. Run status and final-output retrieval after disconnect.
6. Whether client disconnect or gateway death cancels execution.
7. Concurrent runs within a session and total backend capacity.

The PRD's `CreateSession`, `CreateRun`, `SubscribeRun`, and `GetRun` are conceptual operations, not verified endpoints. Adjust the interface to observed capabilities. If only a connection-bound streaming API exists, isolate that transport and explicitly state that Redis metadata persistence does not restore lost execution. Do not silently add a Hermes sidecar or invent durable-run support.

## Failure behavior

| Failure | Required behavior |
| --- | --- |
| Redis unavailable | Reject new admission; never fall back to local counters/state |
| Verifier unavailable/invalid | Fail closed without calling Hermes |
| Hermes create definitively rejected | Preserve audit/request information and release reservation once |
| Hermes start outcome ambiguous | Retain unknown state; reconcile instead of blind retry |
| Stream disconnect | Resume/query if supported; otherwise expose documented recovery limitation |
| Gateway crash | Reload shared state and recover ownership without duplicating execution |
| Discord failure | Track/retry delivery independently; honor platform retry limits |

Bound retries with backoff and jitter. Retry mutating external calls only with a verified safe contract. Shutdown stops admission first, preserves in-flight state, and closes handles without falsely marking runs complete.

## Rendering and observability

Acknowledge accepted work promptly without waiting for final execution. Coalesce progress edits into one status message, honor Discord rate limits, split final output within platform constraints, and disable unintended mass mentions. Relay allowlisted operational/tool statuses and final output, never private chain-of-thought or arbitrary raw tool payloads.

Use structured logs with request, actor, conversation, session, run, policy, duration, status, and delivery identifiers from PRD section 19. Log lifecycle decisions, not secrets or full prompts by default. Preserve original input in the controlled request/run record for configured retention; define access and retention before shared deployment. Redis is not automatically a durable audit archive.

Provide liveness and readiness endpoints. Readiness checks configuration and Redis availability; dependency failures must also be visible in logs without coupling liveness to transient upstream outages. Defer dashboards and telemetry infrastructure; record verifier latency/failures, denials, run transitions, recovery outcomes, and delivery failures in structured events.

## Deployment and rollout

Start on one host with one gateway, persistent Redis, and one Hermes with persistent local volumes. Configure Redis persistence/backups and document the accepted recovery-point objective; application-level statelessness does not guarantee zero data loss. Protect Redis and Hermes on private networking; load secrets from environment/secret management.

Use a small trusted Discord cohort and least-privilege Hermes credentials first. Expand only after denial, long-run, restart, and delivery-failure scenarios pass. Gateway replica scaling does not increase Hermes capacity.

Discord bot ingress normally uses platform connection ownership/sharding, not inbound load-balanced HTTP. Verify the chosen Discord client and deployment mode before activating multiple consumers. Exercise two worker processes sharing Redis separately from the production ingress-ownership decision. No process affinity may be required for logical sessions.

## Review decisions still open

- Select the exact Hermes target and confirm its recoverability ceiling.
- Approve fail-closed `REVIEW`, shared-channel threading, and conversation busy defaults.
- Accept the shared Hermes trust model or require tool enforcement/isolation as a separate scope change.
- Specify expected workload, backend capacity, request/input retention, Redis recovery objective, and first-deployment credentials.

No numerical latency/availability promises are inferred from the PRD. Deployment acceptance uses its explicit observable criteria; operational targets must be chosen before production rollout.

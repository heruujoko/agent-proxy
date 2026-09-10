# PRD: Agent Access Gateway for Hermes

**Status:** V1 Implementation Ready\
**Date:** 2026-09-10\
**Primary implementation:** Go\
**Agent backend:** Hermes Agent\
**State store:** Redis\
**Initial deployment:** 1 Agent Gateway + 1 Hermes\
**Next scale target:** N stateless Agent Gateway instances + 1 Hermes

------------------------------------------------------------------------

## 1. Executive Summary

Build a stateless **Agent Access Gateway** in front of a shared Hermes
Agent used through Discord initially, with Telegram support designed as
an adapter.

The gateway is the control boundary between users and Hermes. It is
responsible for identity resolution, per-user rate limiting, immutable
preservation of the original user request, deterministic validation,
intent/risk verification, policy enforcement, conversation/session
mapping, Hermes run lifecycle management, progress/event relay, and
auditability.

Hermes remains responsible for the agent loop, reasoning, planning, tool
selection, skills, MCP integration, memory, soul, and other Hermes-local
behavior.

V1 deliberately targets **one gateway instance and one Hermes
instance**, but the gateway must be stateless from the beginning so it
can later scale to **N gateway instances + one Hermes** using Redis as
shared coordination/session state.

Horizontal scaling of Hermes itself is explicitly out of scope. Hermes
currently has local/evolving state such as memory, soul,
skills/configuration, MCP configuration, and potentially local files.
Making multiple Hermes replicas behaviorally consistent requires a
separate design.

------------------------------------------------------------------------

## 2. Problem

A Hermes agent directly exposed through Discord or Telegram effectively
trusts every message reaching the agent runtime.

For a company-wide or small multi-user deployment, this creates several
concerns:

-   different users may have different authorization levels;
-   expensive or abusive requests can consume agent/model capacity;
-   user intent should be classified before execution;
-   the original request must not be semantically changed by an
    interceptor;
-   conversations must remain correctly mapped across
    channels/threads/users;
-   long-running Hermes jobs cannot depend on a normal short HTTP
    request lifecycle;
-   progress/tool events should continue to appear in Discord/Telegram;
-   the gateway must eventually support multiple stateless replicas;
-   security decisions must not depend only on an LLM classifier.

The gateway solves the ingress/control-plane portion without modifying
Hermes internals.

------------------------------------------------------------------------

## 3. Goals

### 3.1 V1 goals

1.  Receive Discord messages through a platform adapter.
2.  Resolve the initiating user identity.
3.  Apply per-user rate limits before expensive LLM/Hermes work.
4.  Preserve the original user message verbatim.
5.  Convert platform-specific events into a canonical request envelope
    without rewriting intent.
6.  Run deterministic request validation.
7.  Run an intent/risk verifier using a smaller model with structured
    output.
8.  Apply deterministic policy after classification.
9.  Return DENY responses without sending rejected prompts to Hermes.
10. Resolve/create the Hermes conversation/session for an allowed
    request.
11. Start a Hermes run without coupling execution to a short HTTP
    request timeout.
12. Consume Hermes progress/events through streaming or run-event APIs.
13. Render useful progress back to Discord.
14. Send the final Hermes response to Discord.
15. Store shared session/run/rate-limit state in Redis.
16. Keep the gateway stateless so multiple replicas can later process
    requests.
17. Produce structured audit logs for the complete lifecycle.

### 3.2 Next deployment goal

Support:

``` text
                 Load Balancer
                      |
              +-------+-------+
              |       |       |
             P1      P2      P3
              \       |      /
               \      |     /
                  Redis
                    |
                 Hermes
```

Any proxy instance must be capable of handling any new platform event.

### 3.3 Non-goals

V1 does NOT attempt to:

-   horizontally scale Hermes;
-   synchronize Hermes soul/memory/local files between replicas;
-   replace Hermes memory;
-   redesign Hermes MCP internals;
-   implement full enterprise IAM;
-   implement complex ABAC unless required for a concrete V1 rule;
-   rewrite or sanitize user prompts into a new semantic prompt;
-   expose raw hidden model chain-of-thought;
-   use the intent-verifier LLM as the authorization boundary.

------------------------------------------------------------------------

## 4. Architecture Principles

### 4.1 Gateway is stateless

No correctness-critical state may exist only in process memory.

Local caches are permitted, but the system must remain correct after a
gateway restart or when another gateway instance receives the next
event.

Shared state belongs in Redis and, where durable audit/history is
required later, a persistent database/log sink.

### 4.2 Original input is immutable

The message supplied by the user is the source of truth.

The gateway may annotate it, classify it, resolve mentions, parse
attachments, or derive a cleaned representation, but it must never
replace the original message with an LLM-rewritten version.

Use three distinct concepts:

``` text
OriginalInput
DerivedMetadata
ExecutionContext
```

### 4.3 LLM classifies; policy authorizes

The intent verifier answers:

> What is this user trying to do, and how risky does it appear?

The deterministic policy layer answers:

> Is this actor allowed to make this request?

An LLM output of `PASS` must never grant a capability the user does not
already have.

### 4.4 Long-running agent execution is a run

Do not design Hermes execution as:

``` text
POST -> wait 30-300 seconds -> response
```

Prefer:

``` text
create run -> run_id -> event stream -> completion
```

The lifecycle of the Discord event, gateway request, and Hermes run must
be decoupled.

### 4.5 Progress is observable, not authoritative

Hermes progress/status/tool events can be relayed to the user.

They do not grant authorization.

Do not expose raw private chain-of-thought. Expose operational status,
tool activity, safe reasoning summaries when provided, and final output.

------------------------------------------------------------------------

## 5. High-Level Flow

``` text
                       Discord
                          |
                    Gateway event
                          |
                          v
                +------------------+
                | Agent Gateway    |
                | Go               |
                +--------+---------+
                         |
                         v
                  Ingress Adapter
                         |
                         | preserve original
                         v
                 Identity Resolver
                         |
                         v
                  User Rate Limit
                         |
                         v
              Deterministic Validation
                         |
                         v
                  Intent Verifier
                         |
                  PASS / DENY
                         |
                         v
                      Policy
                         |
                       PASS
                         |
                         v
                Session Resolver
                         |
                         v
                Hermes Run Manager
                         |
                   create run
                         |
                         v
                      run_id
                         |
             +-----------+-----------+
             |                       |
             v                       v
       Save run mapping       Event Subscriber
             |                   SSE/WebSocket
             |                       |
             |          +------------+------------+
             |          |            |            |
             |        status       tools      completion
             |          |            |            |
             |          +------------+------------+
             |                       |
             +-----------------------+
                         |
                         v
                  Discord Renderer
                         |
                         v
                    edit / reply
```

Redis is used by the gateway for shared rate-limit,
conversation/session, and active-run coordination state.

------------------------------------------------------------------------

## 6. Canonical Request Envelope

The platform adapter must create a canonical request without mutating
the original input.

Example:

``` json
{
  "request_id": "req_01J...",
  "actor": {
    "id": "user_123",
    "platform_id": "discord_99122",
    "role": "engineer",
    "groups": ["food-engineering"]
  },
  "source": {
    "platform": "discord",
    "guild_id": "guild_1",
    "channel_id": "channel_2",
    "thread_id": "thread_3",
    "message_id": "message_4"
  },
  "input": {
    "original_text": "<@bot> check food-web but do not modify anything",
    "clean_text": "check food-web but do not modify anything",
    "attachments": []
  },
  "received_at": "2026-09-10T22:10:00+08:00"
}
```

`original_text` MUST be immutable after ingestion.

`clean_text` is optional and may only contain deterministic platform
cleanup such as bot mention removal. It must not contain semantic
rewriting by an LLM.

------------------------------------------------------------------------

## 7. Identity Resolution

Identity must be resolved before rate limiting, intent verification, or
Hermes execution.

V1 may use a static/config-backed mapping:

``` yaml
users:
  discord_99122:
    id: alice
    role: engineer
    groups:
      - food-engineering
```

Future implementations may connect this to company IAM.

Unknown/unmapped users should be denied by default unless explicitly
configured otherwise.

------------------------------------------------------------------------

## 8. Rate Limiting

Rate limiting occurs immediately after identity resolution and before
the intent-verifier model.

This protects both verifier/model cost and Hermes capacity.

V1 should support:

-   per-user burst limit;
-   per-user sustained limit;
-   maximum concurrent active runs per user.

Suggested starting configuration:

``` yaml
rate_limits:
  default:
    requests_per_minute: 10
    requests_per_hour: 100
    max_active_runs: 2
```

These values must be configurable.

Redis keys may follow:

``` text
ratelimit:user:{user_id}:minute
ratelimit:user:{user_id}:hour
active_runs:user:{user_id}
```

Prefer a well-defined token bucket, sliding window, or Redis atomic/Lua
implementation rather than non-atomic read-modify-write logic.

When limited, return a clear user-facing response without invoking the
intent verifier or Hermes.

------------------------------------------------------------------------

## 9. Deterministic Validation

Before the intent verifier:

-   enforce maximum message length;
-   enforce attachment count/size/type limits;
-   reject disabled users;
-   reject malformed requests;
-   validate supported platform/channel context;
-   reject explicitly configured prohibited input patterns where
    deterministic handling is appropriate.

Do not attempt to implement all prompt-injection detection with string
matching.

------------------------------------------------------------------------

## 10. Intent Verifier

Use a smaller/cheaper model to classify the original request.

The verifier MUST use structured output.

Suggested schema:

``` json
{
  "intent": "code_analysis",
  "risk": "medium",
  "requested_capabilities": [
    "github.read"
  ],
  "domains": [
    "food"
  ],
  "prompt_injection_suspected": false,
  "decision": "PASS",
  "confidence": 0.93,
  "reason_codes": [
    "READ_ONLY_CODE_ANALYSIS"
  ]
}
```

Suggested intent enum:

``` text
knowledge_lookup
code_analysis
code_change
deployment
external_communication
administration
unknown
```

Suggested risk enum:

``` text
low
medium
high
critical
```

Suggested verifier decision:

``` text
PASS
DENY
REVIEW
```

The verifier result is **DerivedMetadata** only.

It MUST NOT rewrite the user's prompt.

Low-confidence or `unknown` classifications should fail safely according
to configuration, e.g. deny or require review.

------------------------------------------------------------------------

## 11. Policy Layer

Policy is deterministic.

V1 can start with a simple Go/config policy engine; OPA/Rego can be
introduced if useful without changing the architecture.

Example capability configuration:

``` yaml
roles:
  viewer:
    capabilities:
      - kb.read
      - github.read

  engineer:
    capabilities:
      - kb.read
      - github.read
      - github.issue.create

  maintainer:
    capabilities:
      - kb.read
      - github.read
      - github.write
```

Policy input should include:

-   actor;
-   role/groups;
-   original request identifier;
-   verifier classification;
-   requested capabilities;
-   source/channel metadata.

Policy output:

``` json
{
  "decision": "ALLOW",
  "allowed_capabilities": [
    "github.read"
  ],
  "denied_capabilities": [],
  "reason_codes": [
    "ROLE_ENGINEER",
    "READ_ALLOWED"
  ]
}
```

The gateway must never convert verifier confidence into authorization.

------------------------------------------------------------------------

## 12. Conversation and Session Management

Conversation identity is platform-specific.

Suggested keys:

Discord thread:

``` text
discord:{guild_id}:{channel_id}:{thread_id}
```

Discord DM:

``` text
discord:dm:{user_id}
```

Telegram:

``` text
telegram:{chat_id}
```

For V1, the gateway maps the canonical conversation ID to the
corresponding Hermes session.

Example Redis key:

``` text
session:{conversation_key}
```

Example value:

``` json
{
  "hermes_session_id": "session_xyz",
  "created_at": "...",
  "last_activity_at": "..."
}
```

Recommended semantics:

-   DM -\> per-user conversation;
-   Discord thread -\> shared thread conversation;
-   main shared channel -\> configurable, preferably short-lived or
    explicitly threaded to avoid giant shared contexts.

Hermes may maintain its own internal session conversation state. Redis
stores the mapping/control-plane state, not a competing implementation
of Hermes memory.

------------------------------------------------------------------------

## 13. Hermes Run Lifecycle

For an allowed request:

1.  resolve/create Hermes session;
2.  start a Hermes run;
3.  obtain a `run_id`;
4.  persist the run mapping in Redis;
5.  immediately acknowledge/provide progress to Discord;
6.  subscribe to run events;
7.  update progress as events arrive;
8.  send final output on completion;
9.  mark the run complete and decrement active-run counters.

Conceptual run record:

``` json
{
  "run_id": "gateway_run_001",
  "hermes_run_id": "hermes_run_abc",
  "hermes_session_id": "session_xyz",
  "conversation_key": "discord:g1:c2:t3",
  "actor_id": "alice",
  "platform": "discord",
  "channel_id": "c2",
  "source_message_id": "m3",
  "status_message_id": "m4",
  "status": "running",
  "created_at": "..."
}
```

Suggested Redis key:

``` text
run:{gateway_run_id}
```

Use TTLs where appropriate, but do not expire active runs prematurely.

------------------------------------------------------------------------

## 14. Streaming and Progress Events

The gateway must support long-running Hermes jobs without relying on a
short synchronous HTTP timeout.

Preferred lifecycle:

``` text
POST create-run
     |
     +--> run_id

subscribe(run_id)
     |
     +--> started
     +--> status
     +--> tool.started
     +--> tool.completed
     +--> assistant.delta
     +--> completed
```

SSE is preferred when supported by the Hermes API. WebSocket is
acceptable if required by the actual integration.

The event adapter must translate Hermes-specific events into a stable
internal event model.

Example:

``` go
type AgentEventType string

const (
    EventRunStarted    AgentEventType = "run.started"
    EventStatus        AgentEventType = "status"
    EventToolStarted   AgentEventType = "tool.started"
    EventToolCompleted AgentEventType = "tool.completed"
    EventOutputDelta   AgentEventType = "output.delta"
    EventRunCompleted  AgentEventType = "run.completed"
    EventRunFailed     AgentEventType = "run.failed"
)

type AgentEvent struct {
    Type  AgentEventType
    RunID string
    Text  string
    Tool  *ToolEvent
}
```

Do not make the rest of the gateway depend directly on Hermes event
names.

------------------------------------------------------------------------

## 15. Discord Rendering

Avoid sending a new Discord message for every event.

Preferred UX:

``` text
Working...

✓ Searching knowledge base
✓ Reading repository
→ Analyzing findings...
```

Edit a single progress/status message where practical.

On completion, send or edit to a final response according to Discord
length/format constraints.

The Discord renderer must rate-limit its own message edits so
high-frequency token/event streams do not trigger Discord API rate
limits.

Do not forward raw hidden chain-of-thought. Forward:

-   safe operational status;
-   tool start/completion;
-   subagent status if useful;
-   final assistant output.

------------------------------------------------------------------------

## 16. Redis Responsibilities

Redis is the shared state layer for the stateless gateway.

V1 responsibilities:

``` text
session:{conversation_key}
run:{run_id}
ratelimit:user:{user_id}:*
active_runs:user:{user_id}
```

Optional:

``` text
lock:session:{conversation_key}
dedupe:platform:{message_id}
```

A distributed lock or atomic guard should prevent two gateway workers
from simultaneously creating different Hermes sessions for the same
conversation.

Message deduplication is recommended because platform events can be
retried.

------------------------------------------------------------------------

## 17. Stateless Gateway Requirement

The following must survive gateway restart:

-   conversation -\> Hermes session mapping;
-   active Hermes run mapping;
-   actor/run ownership;
-   platform source/status message identifiers;
-   rate-limit counters;
-   request deduplication state where enabled.

Process memory may contain:

-   HTTP clients;
-   configuration cache;
-   short-lived read-through caches;
-   active stream handles.

Loss of these process-local objects must not corrupt persistent logical
state.

------------------------------------------------------------------------

## 18. Failure Handling

### Intent verifier unavailable

Configurable fail-closed behavior:

``` text
request -> DENY / temporary unavailable
```

Do not bypass the verifier silently.

### Hermes unavailable

Return a user-facing temporary failure and do not lose the original
request/audit record.

### Event stream disconnects

If Hermes supports reconnect/resume or run-status lookup:

1.  keep run mapping;
2.  reconnect or query run status;
3.  continue/finalize rendering.

If Hermes cannot resume streams, document the limitation explicitly.

### Gateway crash

Another gateway instance in the future must be able to recover
run/session metadata from Redis.

V1 with one gateway should still restart without losing conversation
mappings.

### Discord API failure

Keep the Hermes run state independent of Discord rendering failure. Log
failed platform delivery separately.

------------------------------------------------------------------------

## 19. Audit and Observability

Every request should share a correlation/request ID.

Structured logs should cover:

``` text
request.received
identity.resolved
ratelimit.allowed / denied
validation.allowed / denied
intent.completed
policy.allowed / denied
session.resolved / created
run.created
run.event
run.completed / failed
platform.reply.sent / failed
```

Minimum fields:

``` text
request_id
actor_id
conversation_key
platform
intent
risk
policy_decision
hermes_session_id
hermes_run_id
duration_ms
status
```

Never log secrets/tokens.

Be deliberate about whether complete prompt content is logged; support
redaction/configuration for sensitive company use.

------------------------------------------------------------------------

## 20. Security Boundaries

### Input control

``` text
User
  |
Identity
  |
Rate Limit
  |
Validation
  |
Intent Verifier
  |
Policy
  |
Hermes
```

### Action control

The architecture should leave a clear extension point for future
tool/MCP authorization:

``` text
Hermes
  |
proposed tool call
  |
Tool Policy Gateway
  |
actual MCP/tool
```

Tool-level enforcement is desirable, but implementation may be a
follow-up if V1 focuses only on inbound request control.

Do not assume a passed user prompt makes every downstream tool action
safe.

------------------------------------------------------------------------

## 21. Go Package Layout

Suggested structure:

``` text
agent-gateway/
|
+-- cmd/
|   +-- gateway/
|       +-- main.go
|
+-- internal/
|   +-- platform/
|   |   +-- discord/
|   |   |   +-- adapter.go
|   |   |   +-- renderer.go
|   |   +-- telegram/
|   |       +-- adapter.go
|   |
|   +-- request/
|   |   +-- envelope.go
|   |   +-- validation.go
|   |
|   +-- identity/
|   |   +-- resolver.go
|   |
|   +-- ratelimit/
|   |   +-- limiter.go
|   |
|   +-- verifier/
|   |   +-- classifier.go
|   |   +-- schema.go
|   |
|   +-- policy/
|   |   +-- evaluator.go
|   |
|   +-- session/
|   |   +-- store.go
|   |
|   +-- run/
|   |   +-- manager.go
|   |   +-- events.go
|   |
|   +-- agent/
|   |   +-- backend.go
|   |   +-- hermes/
|   |       +-- client.go
|   |       +-- events.go
|   |
|   +-- audit/
|       +-- logger.go
|
+-- config/
|   +-- config.example.yaml
|
+-- docker-compose.yml
+-- Makefile
+-- README.md
```

------------------------------------------------------------------------

## 22. Agent Backend Abstraction

Do not couple the entire gateway to Hermes.

Define a backend interface such as:

``` go
type AgentBackend interface {
    CreateSession(ctx context.Context, req CreateSessionRequest) (Session, error)
    CreateRun(ctx context.Context, req CreateRunRequest) (Run, error)
    SubscribeRun(ctx context.Context, runID string) (<-chan AgentEvent, error)
    GetRun(ctx context.Context, runID string) (Run, error)
}
```

Implement:

``` text
agent/hermes
```

first.

This keeps future OpenClaw or another backend possible without changing
platform, identity, policy, or rate-limit code.

The exact interface SHOULD be adjusted to the actual Hermes API
discovered during implementation.

------------------------------------------------------------------------

## 23. Configuration

Example:

``` yaml
server:
  listen: ":8080"

redis:
  address: "redis:6379"

discord:
  enabled: true
  token_env: "DISCORD_BOT_TOKEN"

telegram:
  enabled: false
  token_env: "TELEGRAM_BOT_TOKEN"

hermes:
  base_url: "http://hermes:8642"
  api_key_env: "HERMES_API_KEY"

verifier:
  provider: "openai-compatible"
  base_url: "..."
  model: "small-classifier-model"
  timeout_seconds: 10
  minimum_confidence: 0.75

rate_limits:
  default:
    requests_per_minute: 10
    requests_per_hour: 100
    max_active_runs: 2
```

Secrets MUST come from environment/secret management, not committed
configuration.

------------------------------------------------------------------------

## 24. Deployment

### V1

``` text
docker-compose / single host

+----------------+
| agent-gateway  |
+-------+--------+
        |
+-------v--------+
| Redis          |
+----------------+

+----------------+
| Hermes         |
| local state    |
+----------------+
```

Hermes local volumes should be persistent.

### Scale target

``` text
                    LB
              +-----+-----+
              |           |
              v           v
          Gateway 1   Gateway 2
              \           /
               \         /
                  Redis
                    |
                  Hermes
```

No gateway instance affinity should be required.

------------------------------------------------------------------------

## 25. Hermes Horizontal Scaling Caveat

Do not implement N Hermes as part of this PRD.

Hermes instances may not be interchangeable because state can evolve
locally:

``` text
Hermes A
  soul version A
  memory version A
  MCP/config version A
  local state A

Hermes B
  soul version B
  memory version B
  MCP/config version B
  local state B
```

Simply adding replicas can create stale or divergent agents.

Before N Hermes is introduced, separately design:

-   soul/config distribution;
-   memory consistency/ownership;
-   MCP/skill version distribution;
-   local file synchronization or externalization;
-   session/run affinity;
-   upgrade/version rollout strategy.

Until then, the supported scale path is:

``` text
1 Gateway + 1 Hermes
        |
        v
N Gateway + 1 Hermes
```

------------------------------------------------------------------------

## 26. Acceptance Criteria

V1 is complete when all of the following pass.

### Ingress

-   Discord user can send a message to the bot.
-   Original message is stored/passed internally without semantic
    rewriting.
-   Actor identity is resolved before model execution.

### Rate limiting

-   User exceeding burst/sustained limit is rejected before
    verifier/Hermes.
-   User exceeding concurrent-run limit cannot start another run.
-   Counters are stored in Redis.

### Intent and policy

-   Allowed prompt produces structured verifier metadata.
-   Denied prompt never reaches Hermes.
-   Policy can deny a request even when verifier returns PASS.
-   Verifier output cannot grant unauthorized capability.

### Session

-   First message creates/resolves a Hermes session.
-   Follow-up in the same Discord conversation maps to the same Hermes
    session.
-   Different configured conversation boundaries do not accidentally
    share sessions.
-   Gateway restart does not lose the mapping.

### Long-running execution

-   A Hermes task taking \>30 seconds completes successfully.
-   The initial platform handling does not wait on one short HTTP
    response.
-   Progress events can be rendered while the run is active.
-   Final response is delivered when Hermes completes.

### Statelessness

-   Restarting the gateway does not lose Redis-backed logical state.
-   A second gateway instance can be started against the same Redis
    without requiring process-local session state.

### Observability

-   A request can be traced from Discord event through
    verifier/policy/session/run to final response using a correlation
    ID.
-   Secrets are not written to logs.

------------------------------------------------------------------------

## 27. Suggested Implementation Order for Codex

Implement vertically rather than building every abstraction first.

### Milestone 1 - Skeleton

-   Go service
-   configuration
-   Redis client
-   health/readiness endpoints
-   structured logging

### Milestone 2 - Discord ingress

-   Discord adapter
-   canonical request envelope
-   identity mapping
-   deterministic validation
-   echo response

### Milestone 3 - Rate limits

-   Redis-backed per-user minute/hour limits
-   active-run concurrency limit
-   tests for atomic behavior

### Milestone 4 - Intent verifier

-   structured classifier schema
-   model client
-   timeout/error handling
-   PASS/DENY/REVIEW behavior
-   mock verifier for tests

### Milestone 5 - Policy

-   capability model
-   deterministic evaluator
-   configuration
-   deny-before-Hermes tests

### Milestone 6 - Hermes adapter

Before coding against assumptions, inspect the installed/target Hermes
version and verify its actual:

-   API server endpoints;
-   session creation semantics;
-   run creation semantics;
-   event-stream protocol;
-   reconnect/status behavior;
-   authentication.

Then implement the `AgentBackend` interface against the verified API.

### Milestone 7 - Session mapping

-   conversation-key resolver
-   Redis session store
-   atomic session creation/lock
-   follow-up conversation tests

### Milestone 8 - Run manager

-   create run
-   persist mapping
-   consume stream
-   lifecycle state
-   active-run accounting
-   failure cleanup

### Milestone 9 - Discord renderer

-   immediate working/status message
-   throttled progress edits
-   final response
-   failure response
-   long-response handling

### Milestone 10 - Recovery and hardening

-   dedupe platform messages
-   stream reconnect/status lookup where Hermes supports it
-   gateway restart tests
-   multiple gateway instances against one Redis
-   race/concurrency tests
-   observability

------------------------------------------------------------------------

## 28. Required Tests

At minimum:

``` text
unit:
  request envelope preserves original input
  deterministic validation
  identity resolution
  rate-limit decisions
  intent schema parsing
  policy decisions
  conversation-key generation
  Hermes event translation

integration:
  Redis rate limiting
  session mapping
  duplicate message handling
  active-run accounting
  mocked Hermes run >30 seconds
  event stream disconnect
  gateway restart
  two gateway processes sharing Redis

security:
  verifier PASS + policy DENY
  unknown user
  malformed verifier output
  verifier timeout
  oversized prompt
  unauthorized capability request
  concurrent-run abuse
```

Use interfaces/mocks so tests do not require a real Discord server or
LLM.

------------------------------------------------------------------------

## 29. Open Questions Codex Must Verify

Do not invent Hermes API behavior.

During implementation, verify against the exact Hermes version being
deployed:

1.  Does Hermes expose persistent server-side sessions in the selected
    version?
2.  What endpoint creates/continues a session?
3.  What endpoint creates a durable/long-running run?
4.  Is progress delivered using SSE, WebSocket, or another mechanism?
5.  Can an event stream reconnect to an existing run?
6.  Can run status/final output be fetched after disconnect?
7.  Does client disconnect cancel execution?
8.  What operational/thinking/tool events are exposed?
9.  What event content is safe/useful to relay versus internal-only?
10. What authentication mechanism does the Hermes API use?

If durable run APIs are unavailable, implement the closest supported
streaming adapter behind `AgentBackend` and document the recovery
limitation rather than leaking Hermes-specific behavior into the rest of
the gateway.

------------------------------------------------------------------------

## 30. Future Extensions

Explicitly not required for V1:

-   Telegram adapter activation;
-   OPA/Rego policy engine;
-   tool/MCP authorization gateway;
-   human approval workflow;
-   role-specific rate limits;
-   Postgres audit/history;
-   admin UI;
-   metrics dashboard;
-   distributed queue;
-   N Hermes;
-   distributed Hermes memory/soul;
-   credential broker;
-   company IAM/SSO integration.

The architecture should leave extension points for these without
implementing them prematurely.

------------------------------------------------------------------------

## 31. Definition of Done

The system is considered ready for the first shared deployment when:

> A known Discord user can send a request, the stateless Go gateway
> resolves identity, rate-limits the user, preserves the original
> prompt, classifies intent, applies deterministic policy, maps the
> Discord conversation to Hermes, starts and observes a potentially
> long-running Hermes execution, relays useful progress, and returns the
> final answer; all correctness-critical gateway state is stored in
> Redis, and a gateway restart does not destroy conversation/session
> mapping.

The initial production topology is intentionally:

``` text
1 Agent Gateway
1 Redis
1 Hermes
```

The immediate scalability target is:

``` text
N stateless Agent Gateways
1 shared Redis
1 Hermes
```

Scaling Hermes itself is a separate future architecture problem.

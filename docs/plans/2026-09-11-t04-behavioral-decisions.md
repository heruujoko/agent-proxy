# T04 — Behavioral and State Decisions

**Date:** 2026-09-11
**Status:** Decisions recorded with the user; identity-provider interface refinement pending the user's validation-layer document (at T08).
**Supersedes:** The "busy response without queueing" proposal in [system design](2026-09-10-gateway-design.md) §Conversations and the PRD's static config-backed identity mapping (§7). Everything else confirms existing proposals.

## Decisions

### D1 — Fail-closed verifier outcomes: deny all
`REVIEW`, `unknown` intent, confidence below threshold, and `prompt_injection_suspected: true` all return a non-executing denial. No approval queue, no bypass capability, no configurable low-confidence execution in V1.

### D2 — Main shared channels: mention required
Messages in a main (non-thread) shared channel are admitted only when they @mention the bot; a non-mention message receives a rate-limited hint reply pointing to threads/mentions and never enters the pipeline. Thread and DM messages are always admitted regardless of mention. DM → per-user conversation; thread → shared conversation.

### D3 — Busy conversation: queue one pending message
When a conversation has an active run, one new message per conversation is held in a single pending slot (newest replaces an older queued message); further messages while the slot is full get a busy response. The pending message starts after the active run reaches a terminal outcome. The slot expires after 15 minutes (configurable) without execution; expiry is an admission denial, not a run.

*Supersedes* the design's "return busy, no queue" default. Affected tasks: T12 (reservation must include pending-slot transitions), T10 (pending state is admission stage), T19 (in-flight shutdown must not lose the slot).

### D4 — Quota charging: early, no refund
Burst/sustained budgets are charged once per admitted logical request, at admission (after dedupe ownership and identity resolution), before the verifier runs. Validation/policy/verifier denial does not refund. Duplicate takeover after a worker crash never re-charges (quota outcome is persisted with the admission stage).

### D5 — Rate-limit algorithm: sliding window
Redis sorted-set sliding windows per user, minute and hour, checked and incremented in one Lua script (atomic). Values configurable (suggested starting point: 10/min, 100/hour, `max_active_runs: 2`).

### D6 — Duplicate window: 15 minutes
Terminal request/dedupe records expire 15 minutes after the terminal outcome. In-flight requests never expire on the dedupe TTL alone.

### D7 — Identity: SQL store + Redis cache, behind a provider interface
Identity lives in PostgreSQL (owned and populated by a **separate service**; migrations/seed data are out of gateway scope), with a Redis TTL cache (~60s, configurable). Semantics:

- Cache miss → read SQL → populate cache.
- SQL unreachable → deny unknown users (**fail closed**); no stale-cache serving.
- Roles/groups never derive from user text.

**Gateway owns only the caller interface** (adapter pattern, multiple user providers possible):

```go
// internal/identity provider seam — final shape refined against the user's
// validation-layer document at T08.
type Provider interface {
    // ResolveByPlatformID returns the trusted identity for a platform user.
    // notFound is a distinct outcome from transport error; errors fail closed.
    ResolveByPlatformID(ctx context.Context, platform, platformID string) (Identity, bool /*found*/, err error)
}
```

Contract: deterministic, fail-closed on error, cacheable, no roles/groups from request input. Concrete Postgres/Redis implementation and schema are deferred to T08 and owned with the user's document.

### D8 — Size units: code points + bytes
Message text limits count Unicode code points. Attachment limits count bytes with a per-type allowlist. Both configurable.

### D9 — Policy: whitelist union only
No deny rules exist in V1. A user's allowed capabilities = union of every whitelist attached to their roles, groups, and any direct user grants. Denial = requested capability absent from the union. Disabled/unknown users rejected before policy. Verifier confidence can never add to the union (D1 applies). Missing capability mappings deny.

### D10 — Retention: 6h prompts, 15m dedupe, no in-flight TTL
- In-flight and pending-delivery records: no TTL; expire only on terminal outcome.
- Terminal dedupe/request records: 15 minutes (D6).
- Terminal raw prompts and reply payloads: 6 hours (crash-between-completion-and-delivery recovery window).
- All values configurable.

### D11 — Configuration model: TOML, decisions are defaults

Mechanism lives in code; every configurable value lives in a TOML file with a shipped default. The values in D1–D10 above are the **shipped defaults**, not hardcoded constants — each deployment can override them. Validation follows the merged foundation's strictness contract: unknown keys rejected, invalid values rejected at startup, defaults apply only to omitted keys, secrets stay environment-only. No deny-by-code-path exceptions: if a rule exists, it's a config value.

```toml
# All values shown are the shipped defaults (= the T04 decisions).

[server]                    # migrated from YAML when the first rules consumer lands
listen = "127.0.0.1:8080"

[rate_limits.default]       # D4, D5
requests_per_minute = 10
requests_per_hour = 100
max_active_runs = 2

[validation]                # D8
max_message_codepoints = 4000
max_attachments = 5
max_attachment_bytes = 10000000
allowed_attachment_types = ["image/png", "image/jpeg", "application/pdf"]

[verifier]                  # D1
min_confidence = 0.85
deny_review = true
deny_unknown_intent = true
deny_prompt_injection_suspected = true

[channels]                  # D2
main_channel_require_mention = true
hint_reply = true

[conversation]              # D3
busy_queue_pending = true
pending_slot_ttl = "15m"

[dedupe]                    # D6
terminal_retention = "15m"

[retention]                 # D10
terminal_prompt_ttl = "6h"

[identity]                  # D7
cache_ttl = "60s"
fail_closed = true

[policy.roles.engineer]     # D9 — whitelists only, union semantics
capabilities = ["kb.read", "github.read", "github.issue.create"]

[policy.groups.food-engineering]
capabilities = ["github.read"]
```

Ownership split: numeric limits, TTLs, windows, channel behavior, and capability whitelists live in TOML (versioned with the gateway, reviewable diffs). *Who* has which role/group and the disabled flag stay in the SQL identity provider (D7) — the gateway never assigns roles. Union evaluation, sliding-window enforcement, and TTL application are pure code.

Format decision: one pinned TOML parser dependency (stdlib has none); `server`/`redis`/`log` migrate from YAML in the same change that introduces the first rules consumer. Exact default numbers above are starting points recorded for review; every one is overridable.

## Open items
- **Identity provider document** — user will supply a validation-layer document; refine D7's interface against it when T08 starts. T08 asks the user for it before design.
- Numeric quota/capacity values remain deployment rollout gates (T25).
- No live probes performed; live verification stays in T26.

# Agent Access Gateway for Hermes

A stateless control plane and ingress gateway sitting between user platforms (Discord, Telegram) and a shared Hermes Agent backend.

The gateway manages identity resolution, rate limiting, deterministic validation, intent/risk verification, policy enforcement, session mapping, asynchronous run lifecycle management, and event relay back to the messaging platform.

---

## Architecture Overview

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

### Key Principles

1. **Stateless Gateway**: No critical state in process memory. All coordination, rate-limiting, session mapping, and active run states live in Redis. Horizontally scalable ($N$ Gateways $\rightarrow$ 1 Hermes).
2. **Immutable Input**: Original user inputs are preserved verbatim. Interceptors never rewrite or mutate user prompts.
3. **LLM Classifies, Policy Authorizes**: A lightweight model classifies intent and risk; deterministic policy decides whether to allow, deny, or escalate.
4. **Decoupled Run Lifecycle**: Runs are asynchronous jobs tracked via run IDs and event streams (SSE/WebSocket), never tied to short HTTP request timeouts.
5. **Safe Progress Relay**: Operational status and tool events are relayed back to the chat platform without exposing private chain-of-thought.

---

## Request Flow

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

---

## Tech Stack

- **Language:** Go
- **Backend Agent:** Hermes Agent
- **State Store:** Redis
- **Supported Ingress:** Discord (V1), Telegram (Adapter planned)

---

## License

[MIT](LICENSE) © 2026 Heru Joko P. Utomo

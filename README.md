# chora-companion-chat

The Learning Companion's chat agent (ADR-254 D2/D6/D8, ex familiar): a Go ADK
agent crew that serves the `companion_chat` dispatch role **subscriber-only**.
It consumes agent-dispatch requests from NATS JetStream, runs the per-Companion
chat agent in-process, and publishes the completion envelope back onto the
reply subject. There is no ADK web launcher and no HTTP surface except the
health port (`/healthz`, `/readyz` on `AGENT_HEALTH_PORT`, default `8080`).

Module path: `github.com/apollo-chora/chora-companion-chat`.

The crew is cloud-neutral: NATS JetStream for events (via
`chora-common/eventbus`), standard OTLP for traces (via `chora-common/otel`),
env-backed secrets (via `chora-common/secrets`), and the local model gateway
(`chora-model-gateway` through `chora-adk-common/modelgatewayclient`) for
model calls. No cloud account or managed service is required.

## Turns

Every dispatch carries a `turn_kind` in its payload:

- **typed** — a learner's message in a persistent conversation. The ADK session
  is keyed on `conversation_id` and lives in Postgres, so two replicas serve
  consecutive turns of one conversation.
- **voice** — the voiced Growth-Edge diagnosis from the companion-diagnosis
  crew, joined onto the learner's conversation.
- **reflect** — the ADR-235 per-goal reflection, with the honest `NO_MEMORY_YET`
  decline and the 600-char contract.

Every turn answers with one JSON envelope:

```json
{"turn_kind": "...", "reply_text": "...", "grounding": [], "tool_calls": [],
 "model_id": "...", "prompt_version": "...", "prompt_source": "..."}
```

## Layout

```
├── cmd/companion_chat/        # entry point — boot wiring only
├── internal/agent/            # the per-Companion chat agent: prompt
│                               # composition, few-shots, growth/memory weave,
│                               # turn router, envelope contract
├── internal/boot/             # config, session store, plugin chain, run
├── internal/skillregistry/    # Familiar (Companion) config registry
│                               # (stub + state-backed)
├── internal/tool/             # cite_atom (chora-creation) and
│                               # weakness.read (chora-consumption) tools
└── agent_card.yaml            # A2A v1.0 Agent Card (POC-era artifact)
```

## Dependencies

| Dependency | Purpose |
|---|---|
| `github.com/apollo-chora/chora-adk-common` | dispatch subscriber, model-gateway LLM adapter, plugin chain pieces, OTel wiring |
| `github.com/apollo-chora/chora-common` | env-backed secrets, event bus, OTel |
| `github.com/apollo-chora/chora-contracts/gen/go` | consumption + creation gRPC contracts (tool clients) |
| `google.golang.org/adk` | the ADK agent framework (not a cloud dependency) |

## Configuration

| Variable | Purpose | Default |
| --- | --- | --- |
| `CHORA_PROJECT` | project label for secret resolution | `chora-local` |
| `CHORA_LOCATION` | location label stamped on the boot line | `local` |
| `COMPANION_CHAT_MODEL` | logical model id passed to the model gateway | `gemini-2.5-flash` |
| `COMPANION_CHAT_SESSIONS_DSN_SECRET_ID` | env-backed secret name of the Postgres DSN for the ADK session store | required |
| `COMPANION_CHAT_SESSIONS_SCHEMA` | session schema (`search_path`) in the database | `companion_chat_sessions` |
| `CHORA_GATEWAY_ENDPOINT` | model gateway gRPC endpoint | `gateway.chora.site:443` |
| `CHORA_GATEWAY_TENANT_ID` / `CHORA_GATEWAY_GCID` | process fallback for the gateway client (per-request values from the dispatch win) | required |
| `CONSUMPTION_GRPC_ENDPOINT` | consumption gRPC (`weakness.read`); `stub://` = tool absent | `stub://chora-consumption` |
| `CREATION_GRPC_ENDPOINT` | creation gRPC (`cite_atom` validator); `stub://` = stub validator | `stub://chora-creation` |
| `CHORA_ENV` | `dev` \| `staging` \| `prod` | `dev` |
| `AGENT_DISPATCH_ENABLED` | must be `true` — the dispatch subscriber is the only transport | — |
| `NATS_URL` | NATS JetStream broker URL | required at serve time |
| `AGENT_DISPATCH_SUBSCRIPTION` | consumer name override | derived from service + role |
| `AGENT_HEALTH_PORT` | health/readiness port | `8080` |

## Runtime dependencies

- **Postgres** — required. The ADK session store is Postgres, never in-memory
  (ADR-254 D5): a chat agent with an in-memory store would forget the
  conversation on every second replica. The DSN comes from the env-backed
  secret named by `COMPANION_CHAT_SESSIONS_DSN_SECRET_ID`
  (`SECRET_<NAME>`, `<NAME>`, or the raw name). The store must be in the
  migrated shape (four tables — `sessions`, `events`, `app_states`,
  `user_states` — each with a `tenant_id` column, RLS enabled and forced, and
  at least one policy) or the boot fails.
- **NATS** — required at serve time. The dispatch subscriber consumes
  `chora.ai_kernel.agent_dispatch.companion_chat_requested.v1` from the
  `CHORA_EVENTS` stream and publishes completions onto the reply subject.
- **chora-model-gateway** — required for model calls (the crew has no other
  model adapter).

## Build and test

```sh
go build ./...
go vet ./...
go test ./...
```

The suite is hermetic — no broker, database, or network is required.

## Docker

```sh
docker buildx build --platform=linux/amd64 -f Dockerfile \
  --build-arg SERVICE_NAME=chora-companion-chat \
  --build-arg GIT_SHA=$(git rev-parse --short HEAD) \
  --build-arg BUILD_TIME=$(date -u +%Y-%m-%dT%H:%M:%SZ) \
  -t walfa/chora-companion-chat:latest .
```

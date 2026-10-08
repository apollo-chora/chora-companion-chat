# chora-companion-chat

## About

`chora-companion-chat` is a Go service that runs the Chora Learning Companion chat agent. It consumes `companion_chat` agent-dispatch requests from NATS JetStream, runs a per-Companion Google ADK agent, and publishes a JSON completion envelope to the dispatch reply subject. The service is subscriber-only; its HTTP surface is limited to health and readiness endpoints.

The agent supports three turn kinds: `typed` (normal conversation), `voice` (voices a Growth-Edge diagnosis), and `reflect` (per-goal reflection). Typed turns use a Postgres-backed ADK session store keyed by `conversation_id`; model calls go through `chora-model-gateway`.

## Quick start

Prerequisites:

- Go 1.26.6
- A Postgres database containing the migrated `companion_chat_sessions` session tables
- A NATS JetStream broker
- A reachable `chora-model-gateway` instance
- An env-backed secret containing the Postgres DSN

Clone the repository and run the test suite:

    git clone https://github.com/apollo-chora/chora-companion-chat.git
    cd chora-companion-chat
    go test ./...

To build and run the binary:

    go build ./cmd/companion_chat
    ./companion_chat

The binary takes no command-line arguments.

## Usage

The service is configured through environment variables. At minimum, set the gateway scope, session DSN secret name, and NATS URL:

    export CHORA_GATEWAY_TENANT_ID=<tenant-id>
    export CHORA_GATEWAY_GCID=<gcid>
    export COMPANION_CHAT_SESSIONS_DSN_SECRET_ID=<secret-name>
    export NATS_URL=<nats-url>
    export AGENT_DISPATCH_ENABLED=true

The main configuration variables are:

| Variable | Default | Description |
| --- | --- | --- |
| `CHORA_PROJECT` | `chora-local` | Project label passed to the env-backed secrets client. |
| `CHORA_LOCATION` | `local` | Location label used in boot configuration. |
| `COMPANION_CHAT_MODEL` | `gemini-2.5-flash` | Logical model ID sent to the model gateway. |
| `COMPANION_CHAT_SESSIONS_DSN_SECRET_ID` | required | Name of the secret containing the Postgres DSN. |
| `COMPANION_CHAT_SESSIONS_SCHEMA` | `companion_chat_sessions` | Postgres schema containing the ADK session tables. |
| `CHORA_GATEWAY_ENDPOINT` | `gateway.chora.site:443` | Model gateway gRPC endpoint. |
| `CHORA_GATEWAY_AUDIENCE` | `https://gateway.chora.site` | ID-token audience used for gateway calls. |
| `CHORA_GATEWAY_TENANT_ID` | required | Process fallback tenant ID for the gateway client; request values take precedence. |
| `CHORA_GATEWAY_GCID` | required | Process fallback GCID for the gateway client; request values take precedence. |
| `CONSUMPTION_GRPC_ENDPOINT` | `stub://chora-consumption` | Consumption gRPC endpoint. A `stub://` value disables the `weakness.read` tool and uses the stub Companion registry. |
| `CREATION_GRPC_ENDPOINT` | `stub://chora-creation` | Creation gRPC endpoint used by `cite_atom`. |
| `CHORA_ENV` | `dev` | Runtime environment label: `dev`, `staging`, or `prod`. |
| `AGENT_DISPATCH_ENABLED` | must be `true` | Enables the only transport used by this service. |
| `NATS_URL` | required | NATS JetStream broker URL. |
| `AGENT_DISPATCH_SUBSCRIPTION` | derived | Optional override for the dispatch consumer name. |
| `AGENT_HEALTH_PORT` | `8080` | Port for `/healthz` and `/readyz`. |

At serve time, the subscriber consumes `chora.ai_kernel.agent_dispatch.companion_chat_requested.v1` from the `CHORA_EVENTS` JetStream stream and publishes the completion envelope onto the reply subject.

A completion has this shape:

    {"turn_kind":"typed","reply_text":"...","grounding":[],"tool_calls":[],"model_id":"...","prompt_version":"...","prompt_source":"...","prompt_hash":"..."}

The `prompt_source` field is `registry` when a prompt override is present and `embedded_fallback` otherwise. `grounding` contains confirmed atom citations produced by `cite_atom`; a failed atom validation is not emitted as grounding.

Health checks are available on the configured health port:

    curl http://localhost:8080/healthz
    curl http://localhost:8080/readyz

The Docker image can be built from the repository root:

    docker buildx build --platform=linux/amd64 -f Dockerfile       --build-arg SERVICE_NAME=chora-companion-chat       --build-arg GIT_SHA=$(git rev-parse --short HEAD)       --build-arg BUILD_TIME=$(date -u +%Y-%m-%dT%H:%M:%SZ)       -t walfa/chora-companion-chat:latest .

## Development

The repository is a standalone Go module:

    module github.com/apollo-chora/chora-companion-chat

The main entry point is `cmd/companion_chat/main.go`. The implementation is split across:

    cmd/companion_chat/       executable entry point
    internal/agent/           turn handling, prompts, memory and Companion behaviour
    internal/boot/            configuration, session store, dispatch wiring and plugins
    internal/skillregistry/   per-Companion configuration registry
    internal/tool/            cite_atom and weakness.read adapters

Run the same checks used by CI:

    gofmt -l .
    go mod tidy
    git diff --exit-code -- go.mod go.sum
    go vet ./...
    go test ./...

The test suite is designed to run without a broker or network. A running Postgres instance is only needed when exercising the live subscriber/session-store path.

The session store expects four ADK tables in the configured schema: `sessions`, `events`, `app_states`, and `user_states`. Each must have a `tenant_id` column, row-level security enabled and forced, and at least one RLS policy. The service checks this shape and a tenant GUC round-trip during startup before it begins serving.

// Command companion_chat is the Learning Companion's chat agent (ADR-254 D2/D8,
// ex familiar). It serves the dispatch role companion_chat SUBSCRIBER-ONLY
// (ADR-254 D6) over NATS JetStream: no ADK web launcher, a health port
// (/healthz, /readyz) and nothing else; a subscriber that cannot start ends
// the process non-zero so the pod dies. The binary takes no arguments.
//
// Turns (payload turn_kind, D6 addendum): typed (a learner's message in a
// persistent conversation: the ADK session is keyed on conversation_id and
// lives in Postgres, so two replicas serve consecutive turns), voice (the
// voiced Growth-Edge diagnosis from companion_diagnosis_crew), reflect (the
// ADR-235 per-goal reflection, ex fog goal_knowledge lane, with the honest
// NO_MEMORY_YET decline and the 600-char contract). Every turn answers with
// one JSON envelope {turn_kind, reply_text, grounding, tool_calls,
// refusal_reason?, nothing_to_say?, model_id, prompt_version, prompt_source},
// where prompt_source makes the embedded-default fallback visible (A5).
//
// Per-Companion behaviour is unchanged: the instance config consumption
// resolves rides in session state, the instancedispatch plugin filters tools
// per turn, growth stage / aha moment / memory weave / growth-edge weave all
// compose the prompt as before. Tools: cite_atom (ADR-249) and weakness.read
// (consumption ReadWeakness; absent when CONSUMPTION_GRPC_ENDPOINT is a
// stub). Model calls go through chora-model-gateway stamped
// surface=companion_chat and billed under companion_chat_turn_{tier}: the
// legacy familiar_chat_turn_{tier} consumption stamps until its W4 cut is
// translated per request (W3, ADR-254 D7).
//
// Env vars (NEVER inlined per feedback_no_inline_config):
//
//	CHORA_PROJECT                            project label for secret resolution (default chora-local)
//	CHORA_LOCATION                           location label (default local)
//	COMPANION_CHAT_MODEL                     default gemini-2.5-flash
//	COMPANION_CHAT_SESSIONS_DSN_SECRET_ID    required: env-backed secret name of the Postgres DSN for the ADK session store
//	COMPANION_CHAT_SESSIONS_SCHEMA           default companion_chat_sessions (search_path; dedicated schema in chora_ai_kernel)
//	CHORA_GATEWAY_ENDPOINT                   default gateway.chora.site:443
//	CHORA_GATEWAY_TENANT_ID / _GCID          required (ADR-163; per-request values from the dispatch win)
//	CONSUMPTION_GRPC_ENDPOINT                consumption gRPC (weakness.read); stub:// = tool absent
//	CREATION_GRPC_ENDPOINT                   creation gRPC (cite_atom validator); stub:// = stub validator
//	CHORA_ENV                                dev | staging | prod
//	AGENT_DISPATCH_ENABLED                   must be "true" (no other transport)
//	NATS_URL                                 NATS JetStream broker URL (required at serve time)
//	AGENT_DISPATCH_SUBSCRIPTION              request subscription (chora-companion-chat.agent-dispatch-companion-chat-requested)
//	AGENT_HEALTH_PORT                        health port (default 8080)
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/apollo-chora/chora-companion-chat/internal/boot"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	// SIGTERM (rollout, scale-down) cancels the context: the dispatch
	// subscriber stops receiving, in-flight work finishes, and the process
	// exits 0. Any other way out is an error and exits non-zero (ADR-254 D6).
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	if err := boot.Run(ctx, os.Args[1:]); err != nil {
		// ERROR, not the std logger's INFO: the exit line is the cause.
		slog.Error("companion_chat: fatal", "err", err.Error())
		os.Exit(1)
	}
}

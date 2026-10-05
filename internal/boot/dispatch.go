package boot

import (
	"strings"

	"github.com/apollo-chora/chora-adk-common/agentdispatch"
)

// Dispatch identity: role companion_chat, Deployment chora-companion-chat. The
// request subscription is set explicitly by the Deployment
// (chora-companion-chat.agent-dispatch-companion-chat-requested); the derived
// name is the fallback and is identical.
const (
	DispatchRole = "companion_chat"
	ServiceName  = "chora-companion-chat"
)

// Payload keys the dispatch carries for this role (ADR-254 D6 addendum).
const (
	stateKeyTurnKind        = "turn_kind"
	stateKeyConversationID  = "conversation_id"
	stateKeyMessage         = "message"
	stateKeyFamiliarID      = "familiar_id"
	stateKeyFamiliarConfig  = "familiar_config"
	stateKeyCompanionName   = "companion_name"
	stateKeyLocale          = "locale"
	stateKeyDiagnosisJSON   = "diagnosis_json"
	stateKeyReflectionJSON  = "reflection_json"
	stateKeyTenantID        = "tenant_id"
	stateKeyUserGCID        = "user_gcid"
	stateKeyPromptOverrides = "prompt_overrides_json"
)

// SessionKey keys the ADK session on the CONVERSATION (ADR-254 D6: two
// replicas serve consecutive turns of one conversation, Postgres-backed):
// user id = {tenant}:{gcid} (the fleet convention, tenant isolation in the
// key), session id = the payload's conversation_id. A reflect turn has no
// conversation and declines (ok=false), so it runs in a per-dispatch session
// like any stateless role.
func SessionKey(req agentdispatch.Request) (userID, sessionID string, ok bool) {
	payload := payloadOf(req)
	conv := strings.TrimSpace(payload[stateKeyConversationID])
	if conv == "" {
		return "", "", false
	}
	gcid := strings.TrimSpace(req.GCID)
	if gcid == "" {
		gcid = "anon"
	}
	return strings.TrimSpace(req.TenantID) + ":" + gcid, conv, true
}

// VoiceTrigger is the user turn recorded for a voice turn. A voice turn lives
// in the learner's CONVERSATION (it voices a fresh diagnosis in the dialogue
// the learner later continues), so its user-side event must read as what it
// is in the history the model sees on the next typed turn, never as the
// fleet's bare "BEGIN" token.
const VoiceTrigger = "(The learner just opened a fresh Growth-Edge diagnosis; voice it for them in your own words.)"

// UserMessage is the learner's text on a typed turn and the self-describing
// trigger on a voice turn; a reflect turn runs stateless on the fleet's BEGIN
// trigger with its inputs in state.
func UserMessage(req agentdispatch.Request) string {
	payload := payloadOf(req)
	switch strings.ToLower(strings.TrimSpace(payload[stateKeyTurnKind])) {
	case "", "typed":
		return strings.TrimSpace(payload[stateKeyMessage])
	case "voice":
		return VoiceTrigger
	}
	return ""
}

// payloadOf reads the string fields of the dispatch payload (tolerant: a
// malformed payload reads as empty and the run then fails on its own guards).
func payloadOf(req agentdispatch.Request) map[string]string {
	out := map[string]string{}
	st, err := req.SessionState()
	if err != nil {
		return out
	}
	for k, v := range st {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

package boot

import (
	"fmt"

	"strings"

	"google.golang.org/adk/agent"
	adkmodel "google.golang.org/adk/model"
	"google.golang.org/adk/plugin"
	"google.golang.org/genai"

	"github.com/apollo-chora/chora-adk-common/agentdispatch"
	"github.com/apollo-chora/chora-adk-common/ahamomentplugin"
	"github.com/apollo-chora/chora-adk-common/growthstageplugin"
	"github.com/apollo-chora/chora-adk-common/instancedispatch"
	"github.com/apollo-chora/chora-adk-common/modelgatewayclient"
	"github.com/apollo-chora/chora-adk-common/terminationplugin"
	"github.com/apollo-chora/chora-adk-common/tracing"

	capp "github.com/apollo-chora/chora-companion-chat/internal/agent"
)

// Pinned termination identity (D6 P3 trace-emission contract). The chat agent
// is a ReAct single agent: a turn is at most one tool call, a reflection and
// a final answer; three model calls cover it with one retry.
const (
	terminationRuntime       = "AGENT_EXECUTION_RUNTIME_ADK_GO"
	terminationCrewPattern   = "P1_SINGLE_AGENT_REACT"
	terminationMaxIterations = 3
	// stateKeyManaActionCode is stamped by consumption per interaction with the
	// per-tier chat code. Consumption stamps the legacy familiar_chat_turn_{tier}
	// until its W4 cut; from the W3 hour (ADR-254 D7, identity 0039 twins) the
	// agent bills under companion_chat_turn_{tier}: companionActionCode translates
	// the chat family and passes everything else through untouched.
	stateKeyManaActionCode = "mana_action_code"
	// legacyChatActionCodePrefix / companionChatActionCodePrefix are the two
	// spellings of the chat family: "familiar_chat" and "familiar_chat_turn_*"
	// become "companion_chat" and "companion_chat_turn_*".
	legacyChatActionCodePrefix    = "familiar_chat"
	companionChatActionCodePrefix = "companion_chat"
)

// companionActionCode maps a stamped mana action code onto the companion_*
// twin the gateway meters from W3: the legacy chat family (familiar_chat,
// familiar_chat_turn_{tier}) is renamed by prefix, a companion_* code passes
// through, any other code (or none) is returned as stamped so the gateway's own
// vocabulary rules apply to it unchanged. Whitespace is trimmed; the function
// never invents a tier.
func companionActionCode(code string) string {
	code = strings.TrimSpace(code)
	if strings.HasPrefix(code, legacyChatActionCodePrefix) {
		return companionChatActionCodePrefix + strings.TrimPrefix(code, legacyChatActionCodePrefix)
	}
	return code
}

// StateString reads a string value from ADK session state ("" on miss).
func StateString(state interface {
	Get(string) (any, error)
}, key string) string {
	v, err := state.Get(key)
	if err != nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// NewInboundTracePlugin links the agent's trace to the dispatch envelope's
// W3C traceparent (set in session state by agentdispatch).
func NewInboundTracePlugin() (*plugin.Plugin, error) {
	p, err := plugin.New(plugin.Config{
		Name: "chora_inbound_trace_" + CrewKind,
		BeforeAgentCallback: func(ctx agent.CallbackContext) (*genai.Content, error) {
			if tp := StateString(ctx.State(), "traceparent"); tp != "" {
				tracing.AddInboundLink(ctx, tp, StateString(ctx.State(), "tracestate"))
			}
			return nil, nil
		},
	})
	if err != nil {
		return nil, fmt.Errorf("inbound-trace plugin.New: %w", err)
	}
	return p, nil
}

// TerminationConfig is the pinned termination-plugin configuration.
func TerminationConfig() terminationplugin.Config {
	return terminationplugin.Config{
		Publisher:     &terminationplugin.LoggingPublisher{},
		AgentID:       CrewKind,
		Runtime:       terminationRuntime,
		CrewKind:      CrewSurface,
		CrewPattern:   terminationCrewPattern,
		MaxIterations: terminationMaxIterations,
	}
}

// NewPlugins builds the chain in its runtime order, the familiar chain with
// the inbound trace link in front:
//
//	inboundTrace -> tenantProp (per-request tenant/gcid + per-turn action code)
//	-> instancedispatch (refuses turns without familiar_id; filters tools per
//	Companion) -> familiarIDLabel (billing label + span attributes)
//	-> growthstage -> ahamoment -> termination
//
// No agent-side mana or Armor plugin: the gateway meters and screens
// (ADR-152 / ADR-177 / ADR-254 D12).
func NewPlugins(dispatchCfg instancedispatch.Config) ([]*plugin.Plugin, error) {
	inboundTraceP, err := NewInboundTracePlugin()
	if err != nil {
		return nil, err
	}
	seedP, err := newSeedValidationPlugin()
	if err != nil {
		return nil, err
	}
	tenantPropP, err := modelgatewayclient.NewTenantPropagationPlugin(CrewKind,
		modelgatewayclient.WithActionCodeResolver(func(get func(key string) string) string {
			return companionActionCode(get(stateKeyManaActionCode))
		}))
	if err != nil {
		return nil, fmt.Errorf("modelgatewayclient.NewTenantPropagationPlugin: %w", err)
	}
	innerDispatchP, err := instancedispatch.New(dispatchCfg)
	if err != nil {
		return nil, fmt.Errorf("instancedispatch.New: %w", err)
	}
	dispatchP, err := newTurnAwareDispatchPlugin(innerDispatchP)
	if err != nil {
		return nil, err
	}
	labelP, err := capp.NewFamiliarIDLabelPlugin()
	if err != nil {
		return nil, fmt.Errorf("NewFamiliarIDLabelPlugin: %w", err)
	}
	growthP, err := growthstageplugin.New(growthstageplugin.Config{CrewKind: CrewKind})
	if err != nil {
		return nil, fmt.Errorf("growthstageplugin.New: %w", err)
	}
	ahaP, err := ahamomentplugin.New(ahamomentplugin.Config{CrewKind: CrewKind})
	if err != nil {
		return nil, fmt.Errorf("ahamomentplugin.New: %w", err)
	}
	terminationP, err := terminationplugin.New(TerminationConfig())
	if err != nil {
		return nil, fmt.Errorf("terminationplugin.New: %w", err)
	}
	return []*plugin.Plugin{inboundTraceP, seedP, tenantPropP, dispatchP, labelP, growthP, ahaP, terminationP}, nil
}

// stateKeyGrowthStage is the per-Companion stage the growth-stage plugin
// gates tools and memory on (a JSON number 0-6 on the wire).
const stateKeyGrowthStage = growthstageplugin.StateKeyGrowthStage

// newSeedValidationPlugin refuses, by name and as a PERMANENT failure, a
// dispatch seed the chain would otherwise reject on every redelivery: a
// growth_stage that is present but not a number (the growth-stage plugin
// errors on it, and that error is a plain one the handler would retry five
// times before reporting, seen live 2026-08-22). Runs first, before any
// plugin does work on the turn.
func newSeedValidationPlugin() (*plugin.Plugin, error) {
	p, err := plugin.New(plugin.Config{
		Name: "chora_seed_validation_" + CrewKind,
		BeforeRunCallback: func(ic agent.InvocationContext) (*genai.Content, error) {
			raw, err := ic.Session().State().Get(stateKeyGrowthStage)
			if err != nil {
				return nil, nil // absent: the growth-stage plugin's own default applies
			}
			switch raw.(type) {
			case int, int32, int64, float64:
				return nil, nil
			}
			return nil, agentdispatch.Permanent(
				fmt.Sprintf("invalid_growth_stage: growth_stage must be a JSON number 0-6, got %T", raw), nil)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("seed-validation plugin.New: %w", err)
	}
	return p, nil
}

// newTurnAwareDispatchPlugin wraps the instancedispatch plugin so that only a
// TYPED turn resolves the per-Companion instance config and filters tools per
// Companion. A voice or reflect turn is composed from its own locked template
// and needs the Companion's identity (familiar_id) but not its config, so it
// runs on familiar_id alone with every tool stripped (neither turn calls
// tools). Both refuse by name, as permanent failures, what they cannot run
// without, instead of answering on defaults.
func newTurnAwareDispatchPlugin(inner *plugin.Plugin) (*plugin.Plugin, error) {
	return plugin.New(plugin.Config{
		Name: "chora_turn_aware_instancedispatch",
		BeforeRunCallback: func(ic agent.InvocationContext) (*genai.Content, error) {
			st := ic.Session().State()
			kind, err := capp.ParseTurnKind(StateString(st, stateKeyTurnKind))
			if err != nil {
				return nil, agentdispatch.Permanent(err.Error(), nil)
			}
			if strings.TrimSpace(StateString(st, stateKeyFamiliarID)) == "" {
				return nil, agentdispatch.Permanent("missing_familiar_id: every companion_chat turn names its Companion", nil)
			}
			if kind != capp.TurnTyped {
				return nil, nil
			}
			if strings.TrimSpace(StateString(st, stateKeyFamiliarConfig)) == "" {
				return nil, agentdispatch.Permanent("missing_familiar_config: a typed turn composes from the Companion's instance config", nil)
			}
			if cb := inner.BeforeRunCallback(); cb != nil {
				return cb(ic)
			}
			return nil, nil
		},
		BeforeModelCallback: func(ctx agent.CallbackContext, req *adkmodel.LLMRequest) (*adkmodel.LLMResponse, error) {
			kind, err := capp.ParseTurnKind(StateString(ctx.ReadonlyState(), stateKeyTurnKind))
			if err != nil {
				return nil, agentdispatch.Permanent(err.Error(), nil)
			}
			if kind != capp.TurnTyped {
				if req != nil {
					req.Tools = nil
					if req.Config != nil {
						req.Config.Tools = nil
					}
				}
				return nil, nil
			}
			if cb := inner.BeforeModelCallback(); cb != nil {
				return cb(ctx, req)
			}
			return nil, nil
		},
	})
}

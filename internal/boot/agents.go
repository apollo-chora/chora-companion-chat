package boot

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"strings"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	adkmodel "google.golang.org/adk/model"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
	"google.golang.org/genai"

	"github.com/apollo-chora/chora-adk-common/agentdispatch"
	"github.com/apollo-chora/chora-adk-common/instancedispatch"
	"github.com/apollo-chora/chora-adk-common/promptstamping"

	capp "github.com/apollo-chora/chora-companion-chat/internal/agent"
	"github.com/apollo-chora/chora-companion-chat/internal/skillregistry"
	tools "github.com/apollo-chora/chora-companion-chat/internal/tool"
)

// Agent tree names: the root router answers every turn with the envelope; the
// llmagent under it is the per-Companion chat agent (capp.Name, the terminal
// author for dispatch is "" so the last text event, the router's, wins).
const (
	routerAgentName = CrewKind + "_router"
	toolKeyCiteAtom = "atom.cite"
	// toolNameCiteAtom is the ADK tool NAME (distinct from the skill key
	// above). Declared once: the harvest and the registration must agree, and
	// the original defect was exactly such a pair drifting apart.
	toolNameCiteAtom = "cite_atom"
	toolKeyWeakness  = "weakness.read"
)

// Deps are the collaborators the agent tree needs; run.go builds the real
// ones, tests inject fakes.
type Deps struct {
	LLM            adkmodel.LLM
	Registry       skillregistry.Registry
	CiteValidator  tools.Validator
	WeaknessReader tools.WeaknessReader // nil = tool absent (stub endpoint)
}

// Tree is the composed agent tree plus the instancedispatch config the plugin
// chain shares with the instruction provider.
type Tree struct {
	Root        agent.Agent
	DispatchCfg instancedispatch.Config
	Tools       []tool.Tool
}

// BuildTree composes the companion_chat root: a turn_kind router over the
// per-Companion llmagent (typed), with the voice and reflect turns served by
// the same agent under a different per-turn instruction.
func BuildTree(cfg Config, deps Deps) (Tree, error) {
	if deps.LLM == nil || deps.Registry == nil || deps.CiteValidator == nil {
		return Tree{}, errors.New("BuildTree: LLM, Registry and CiteValidator are required")
	}
	citeAtomTool, err := functiontool.New(functiontool.Config{
		Name:        toolNameCiteAtom,
		Description: "Validate an atom_id against chora-creation before citing it in a reply. Reject fabricated atom_ids.",
	}, func(tctx tool.Context, in tools.CiteAtomRequest) (tools.CiteAtomResponse, error) {
		tenantID := StateString(tctx.State(), stateKeyTenantID)
		if strings.TrimSpace(tenantID) == "" {
			return tools.CiteAtomResponse{}, errors.New("cite_atom: tenant_id absent from session state; refusing un-scoped validation")
		}
		return tools.CiteAtom(tctx, deps.CiteValidator, in, tenantID)
	})
	if err != nil {
		return Tree{}, fmt.Errorf("functiontool.New(cite_atom): %w", err)
	}
	available := map[string]tool.Tool{toolKeyCiteAtom: citeAtomTool}
	allTools := []tool.Tool{citeAtomTool}
	if deps.WeaknessReader != nil {
		// ADR-254 D6 / R3: the diagnosis READ is a tool. Absent when the
		// reader is not wired, never a placeholder.
		reader := deps.WeaknessReader
		weaknessTool, err := functiontool.New(functiontool.Config{
			Name:        "weakness_read",
			Description: "Read the learner's current Growth Edges (concepts to grow next, shakiest first) so you can steer practice toward them. Returns labels and summaries only.",
		}, func(tctx tool.Context, in tools.WeaknessReadRequest) (tools.WeaknessReadResponse, error) {
			st := tctx.State()
			return tools.ReadWeakness(tctx, reader, in, StateString(st, stateKeyTenantID), StateString(st, stateKeyFamiliarID), StateString(st, stateKeyUserGCID))
		})
		if err != nil {
			return Tree{}, fmt.Errorf("functiontool.New(weakness_read): %w", err)
		}
		available[toolKeyWeakness] = weaknessTool
		allTools = append(allTools, weaknessTool)
	}

	resolver, err := capp.NewFamiliarResolver(deps.Registry, available)
	if err != nil {
		return Tree{}, fmt.Errorf("NewFamiliarResolver: %w", err)
	}
	dispatchCfg := instancedispatch.Config{Resolver: resolver, StateKey: stateKeyFamiliarID}
	typedProvider, err := instancedispatch.NewInstructionProvider(dispatchCfg)
	if err != nil {
		return Tree{}, fmt.Errorf("instancedispatch.NewInstructionProvider: %w", err)
	}
	typedProvider = capp.WithGrowthEdgeContext(capp.WithMemoryContext(typedProvider))

	// N9 step 3. The hash already exists: WithStamping takes ContentHash over
	// exactly the rendered instruction and puts it on an OTLP span. This
	// recorder carries that SAME value to the envelope, so nothing recomputes
	// it and no second definition of "the prompt" can drift from the first. It
	// wraps OUTSIDE the stamping provider, so the string hashed is the one the
	// model receives after every weave and override.
	promptHashes := newPromptHashRecorder()
	chat, err := capp.BuildFamiliarAgent(context.Background(), deps.LLM,
		recordPromptHash(promptHashes,
			promptstamping.WithStamping(capp.FamiliarPromptVersion, turnConditions, turnInstructionProvider(typedProvider))),
		allTools)
	if err != nil {
		return Tree{}, fmt.Errorf("BuildFamiliarAgent: %w", err)
	}
	root, err := newRouter(chat, promptHashes)
	if err != nil {
		return Tree{}, err
	}
	return Tree{Root: root, DispatchCfg: dispatchCfg, Tools: allTools}, nil
}

// turnInstructionProvider routes the per-turn instruction by turn_kind:
// typed keeps the per-Companion composition (instance config, memory weave,
// growth-edge weave), voice and reflect render their locked templates.
func turnInstructionProvider(typed llmagent.InstructionProvider) llmagent.InstructionProvider {
	return func(ctx agent.ReadonlyContext) (string, error) {
		st := ctx.ReadonlyState()
		kind, err := capp.ParseTurnKind(StateString(st, stateKeyTurnKind))
		if err != nil {
			return "", agentdispatch.Permanent(err.Error(), nil)
		}
		switch kind {
		case capp.TurnTyped:
			// A typed turn without the learner's text would answer the BEGIN
			// trigger: refuse it by name instead (no redelivery can supply it).
			if strings.TrimSpace(StateString(st, stateKeyMessage)) == "" {
				return "", agentdispatch.Permanent("missing_message: a typed turn carries the learner's text under message", nil)
			}
		case capp.TurnVoice:
			inst, err := capp.ComposeVoiceInstruction(StateString(st, stateKeyCompanionName), StateString(st, stateKeyLocale), StateString(st, stateKeyDiagnosisJSON))
			if err != nil {
				return "", agentdispatch.Permanent("invalid_diagnosis_json", err)
			}
			return inst, nil
		case capp.TurnReflect:
			in, err := capp.ParseReflectionInput(StateString(st, stateKeyReflectionJSON))
			if err != nil {
				return "", agentdispatch.Permanent("invalid_reflection_json", err)
			}
			return capp.BuildReflectionPrompt(in), nil
		}
		return typed(ctx)
	}
}

// turnConditions are the ADR-197 prompt discriminants: the familiar's
// instance conditions plus the turn kind.
func turnConditions(st session.ReadonlyState) map[string]string {
	c := capp.InstanceConditions(st)
	kind, err := capp.ParseTurnKind(StateString(st, stateKeyTurnKind))
	if err != nil {
		kind = "unknown"
	}
	c["turn_kind"] = string(kind)
	return c
}

// newRouter wraps the chat agent: it runs the turn, collects the dialogue
// (tool calls, grounding citations, the model version, the final text),
// applies the reflect contract, and emits the completion envelope as its own
// final text event. Every turn answers with the same shape (ADR-254 D6/D8).
func newRouter(chat agent.Agent, promptHashes *promptHashRecorder) (agent.Agent, error) {
	return agent.New(agent.Config{
		Name:        routerAgentName,
		Description: "companion_chat turn router: runs the Companion and answers with the completion envelope",
		SubAgents:   []agent.Agent{chat},
		Run: func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {
				// Every exit path clears this invocation's recorded hash,
				// including the early refusals below: an entry left behind by a
				// turn that never reached the envelope would leak one string
				// per failed turn for the life of the pod.
				defer promptHashes.take(ctx.InvocationID())
				st := ctx.Session().State()
				kind, err := capp.ParseTurnKind(StateString(st, stateKeyTurnKind))
				if err != nil {
					yield(nil, agentdispatch.Permanent(err.Error(), nil))
					return
				}
				var (
					finalText string
					modelID   string
					toolCalls []capp.ToolCallRef
					grounding []capp.GroundingRef
				)
				for ev, err := range chat.Run(ctx) {
					if err != nil {
						yield(nil, err)
						return
					}
					if ev != nil {
						if ev.ModelVersion != "" {
							modelID = ev.ModelVersion
						}
						if ev.Content != nil && !ev.Partial {
							var sb strings.Builder
							for _, p := range ev.Content.Parts {
								if p == nil {
									continue
								}
								if p.FunctionCall != nil {
									toolCalls = append(toolCalls, capp.ToolCallRef{Name: p.FunctionCall.Name})
								}
								if p.FunctionResponse != nil && p.FunctionResponse.Name == toolNameCiteAtom {
									if ref, ok := citationFrom(p.FunctionResponse.Response); ok {
										grounding = append(grounding, ref)
									}
								}
								sb.WriteString(p.Text)
							}
							if t := sb.String(); t != "" && ev.Author == chat.Name() {
								finalText = t
							}
						}
					}
					if !yield(ev, nil) {
						return
					}
				}
				// Taken exactly once per invocation and removed, so a long-lived
				// pod accumulates nothing and no later turn can inherit this
				// one's hash. Deferred at the top of the run rather than only
				// here, or a turn that yields early would leave its entry
				// behind forever.
				env, err := buildEnvelope(kind, finalText, modelID, toolCalls, grounding,
					StateString(st, stateKeyPromptOverrides), promptHashes.take(ctx.InvocationID()))
				if err != nil {
					yield(nil, err)
					return
				}
				out := session.NewEvent(ctx.InvocationID())
				out.Author = routerAgentName
				out.Content = &genai.Content{Role: "model", Parts: []*genai.Part{{Text: env.Render()}}}
				yield(out, nil)
			}
		},
	})
}

// citationFrom turns a cite_atom FunctionResponse into a disclosed source.
//
// ADR-249 makes cite_atom an ANTI-FABRICATION guard, so a citation is emitted
// ONLY when the validator CONFIRMED the atom for the tenant. A rejected id is
// dropped: citing a fabricated source to a learner is worse than citing
// nothing, and the whole point of running the guard is to act on its verdict.
// The attempt still appears in tool_calls, so a refusal stays visible.
func citationFrom(resp map[string]any) (capp.GroundingRef, bool) {
	if resp == nil {
		return capp.GroundingRef{}, false
	}
	if exists, _ := resp["exists"].(bool); !exists {
		return capp.GroundingRef{}, false
	}
	id, _ := resp["atom_id"].(string)
	if strings.TrimSpace(id) == "" {
		return capp.GroundingRef{}, false
	}
	rev, _ := resp["current_revision_id"].(string)
	return capp.GroundingRef{AtomID: id, RevisionID: rev}, true
}

// buildEnvelope applies the per-turn output contract and the A5 visibility.
func buildEnvelope(kind capp.TurnKind, finalText, modelID string, toolCalls []capp.ToolCallRef, grounding []capp.GroundingRef, overridesJSON, promptHash string) (capp.Envelope, error) {
	env := capp.Envelope{TurnKind: string(kind), ToolCalls: toolCalls, Grounding: grounding, ModelID: modelID,
		PromptVersion: capp.FamiliarPromptVersion, PromptSource: capp.PromptSourceEmbedded,
		PromptHash: promptHash}
	if strings.TrimSpace(overridesJSON) != "" {
		env.PromptSource = capp.PromptSourceRegistry
	}
	switch kind {
	case capp.TurnReflect:
		env.PromptVersion = capp.ReflectionPromptVersion
		text, silent, err := capp.ValidateSynthesis(finalText)
		if err != nil {
			return env, err // transient: a redelivery may comply; the fifth attempt reports FAILED
		}
		env.ReplyText, env.NothingToSay = text, silent
	case capp.TurnVoice:
		env.PromptVersion = capp.VoicePromptVersion
		text := capp.StripWrapping(finalText)
		if text == "" {
			return env, errors.New("voice: model returned an empty message")
		}
		env.ReplyText = text
	default:
		if strings.TrimSpace(finalText) == "" {
			return env, errors.New("typed: the Companion produced no reply text")
		}
		env.ReplyText = finalText
	}
	if modelID == "" {
		slog.Warn("companion_chat: no model version on the final event; envelope model_id is empty", "turn_kind", kind)
	}
	return env, nil
}

package agent

import (
	"context"
	"fmt"
	"log"
	"sort"

	"google.golang.org/adk/tool"

	"github.com/apollo-chora/chora-adk-common/instancedispatch"
	"github.com/apollo-chora/chora-companion-chat/internal/skillregistry"
)

// NewFamiliarResolver adapts a Familiar skill registry into the generic
// `instancedispatch.Resolver` shape used by the dispatch plugin
// (per ADR-147 §7 + POC week-1 discovery #4).
//
// The resolver:
//
//  1. Loads the per-Familiar config from the registry by familiar_id.
//  2. Validates structural invariants via ValidateConfigInvariants
//     (cap + dedup — the structural invariants shared by both deploy
//     variants; kept in lockstep).
//  3. Translates the skill_key allowlist to actual tool.Name() values using
//     the `available` map (single source of truth for skill_key →
//     tool.Tool resolution).
//  4. Returns an InstanceConfig with the composed Instruction prompt + the
//     translated tool name allowlist.
//
// Pair the returned Resolver with:
//   - `instancedispatch.New(...)` for the plugin (filters req.Tools per turn)
//   - `instancedispatch.NewInstructionProvider(...)` for the llmagent's
//     InstructionProvider (composes per-Familiar prompt per turn)
//
// Both call this resolver under the hood — coherent dispatch by construction.
func NewFamiliarResolver(
	registry skillregistry.Registry,
	available map[string]tool.Tool,
) (instancedispatch.Resolver, error) {
	if registry == nil {
		return nil, fmt.Errorf("NewFamiliarResolver: registry is required")
	}
	if available == nil {
		return nil, fmt.Errorf("NewFamiliarResolver: available tool map is required")
	}
	return func(ctx context.Context, familiarID string) (instancedispatch.InstanceConfig, error) {
		cfg, err := registry.LoadFamiliarConfig(ctx, familiarID)
		if err != nil {
			return instancedispatch.InstanceConfig{}, err
		}
		if err := ValidateConfigInvariants(cfg); err != nil {
			return instancedispatch.InstanceConfig{}, fmt.Errorf(
				"familiar %s: %w", cfg.FamiliarID, err)
		}
		// Pre-hatch guard (ADR-149): Stage-0 Familiars (Mystery Egg / unbound)
		// must not participate in chat sessions. RefusePreHatch returns
		// ErrFamiliarPreHatch which propagates through instancedispatch.
		// BeforeRunCallback and refuses the turn before any LLM call is made.
		// Placed AFTER ValidateConfigInvariants so structural violations take
		// precedence in error classification.
		if err := RefusePreHatch(cfg); err != nil {
			return instancedispatch.InstanceConfig{}, err
		}
		// CHO-2013 P1.B (R4-2): the per-turn filter keys on the MERGED
		// allowed_tools list computed by consumption (innate(stage) ∪
		// equipped Skills' bindings). Unknown names are SKIPPED with a loud
		// WARN — narrowing-safe degradation: a catalogue row referencing a
		// tool this agent build does not ship must never kill the familiar's
		// whole chat session; the Skill's tool is simply absent.
		allowedToolNames := make([]string, 0, len(cfg.AllowedTools))
		for _, toolKey := range cfg.AllowedTools {
			t, ok := available[toolKey]
			if !ok {
				log.Printf("WARN instancedispatch: familiar %s allowed tool %q not in this build's registry (have %v) — skipping (R4-2 narrowing-safe)",
					cfg.FamiliarID, toolKey, sortedKeysOfTools(available))
				continue
			}
			allowedToolNames = append(allowedToolNames, t.Name())
		}
		// ADR-197 P3 (CHO-2368): thread the registry-resolved prompt
		// overrides (injected into session state by the session creator,
		// stamped onto ctx by ResolveInstanceFromState) into the composer.
		// Absent / malformed payloads parse to nil - the embedded baseline
		// renders unchanged (fail-soft, bounded drift).
		overrides := parsePromptOverridesJSON(
			instancedispatch.PromptOverridesJSONFromContext(ctx))
		return instancedispatch.InstanceConfig{
			Instruction:  ComposeInstructionWithOverrides(cfg, overrides),
			AllowedTools: allowedToolNames,
		}, nil
	}, nil
}

// ValidateConfigInvariants enforces the structural invariants of a Familiar
// config that apply to both deploy variants (per-session AND dispatch-plugin):
//
//   - Non-nil config
//   - len(AllowedSkills) ≤ SkillSlotsUnlocked (XP-gated capacity cap)
//   - No duplicate skill keys in AllowedSkills
//
// Pure function — testable without context/registry. The per-session
// deploy variant's filter added a presence-in-`available` check;
// NewFamiliarResolver (dispatch-plugin path; presence check is
// done downstream in the resolver body).
func ValidateConfigInvariants(cfg *skillregistry.FamiliarConfig) error {
	if cfg == nil {
		return fmt.Errorf("ValidateConfigInvariants: cfg is nil")
	}
	if len(cfg.AllowedSkills) > cfg.SkillSlotsUnlocked {
		return fmt.Errorf(
			"ValidateConfigInvariants: %d allowed skills > %d slots unlocked",
			len(cfg.AllowedSkills), cfg.SkillSlotsUnlocked)
	}
	seen := make(map[string]struct{}, len(cfg.AllowedSkills))
	for _, key := range cfg.AllowedSkills {
		if _, dup := seen[key]; dup {
			return fmt.Errorf("ValidateConfigInvariants: duplicate skill key %q", key)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func sortedKeysOfTools(m map[string]tool.Tool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

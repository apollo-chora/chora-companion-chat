package agent

import (
	"strings"
	"testing"

	"github.com/apollo-chora/chora-companion-chat/internal/skillregistry"
)

// ADR-249 A1a: the rendered system prompt must name ONLY tools the agent
// exposes, in the model-visible vocabulary. The ladder vocabulary
// (atom_search / persona_lookup / ebbinghaus_state / score_atom_for_learner)
// is a growth-gating namespace the model cannot see, and three of those
// four never shipped a working tool at all.

func TestComposedPromptNamesOnlyExposedToolNames(t *testing.T) {
	cfg := &skillregistry.FamiliarConfig{
		Name:           "TestFam",
		Specialization: "math",
		GrowthStage:    4,
		Species:        "owl",
		ConfiguredRules: skillregistry.ConfiguredRules{
			CitationStrictness: "strict",
		},
		AllowedTools: []string{"atom.cite"},
	}
	prompt := ComposeInstructionWithOverrides(cfg, nil)

	for _, ladderOnly := range []string{
		"atom_search", "persona_lookup", "ebbinghaus_state", "score_atom_for_learner",
	} {
		if strings.Contains(prompt, ladderOnly) {
			t.Errorf("prompt names %q, which is not an exposed tool name (ladder vocabulary leak)", ladderOnly)
		}
	}
	if !strings.Contains(prompt, "cite_atom") {
		t.Errorf("prompt must direct the model at the surviving cite_atom tool when atom.cite is allowed")
	}
}

func TestComposedPromptOmitsCiteLineWhenToolNotAllowed(t *testing.T) {
	cfg := &skillregistry.FamiliarConfig{
		Name:           "EggFam",
		Specialization: "math",
		GrowthStage:    1,
		Species:        "owl",
		ConfiguredRules: skillregistry.ConfiguredRules{
			CitationStrictness: "strict",
		},
		AllowedTools: nil,
	}
	prompt := ComposeInstructionWithOverrides(cfg, nil)
	if strings.Contains(prompt, "cite_atom") {
		t.Errorf("prompt must not name cite_atom when atom.cite is not in the allowed tool set")
	}
}

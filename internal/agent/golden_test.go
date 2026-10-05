package agent

// golden_test.go - frozen composed-instruction golden for the Familiar
// (CHO-2368, ADR-197 catalogue). Same discipline as the qgen/OE goldens: the
// golden is the byte-pinned ComposeInstruction rendering of a canonical staged
// config with NO overrides. The ADR-197 baseline seed generator (orchestrator
// domain/prompt_registry/baseline_seedspec.py) regex-pins its familiar
// catalogue segment TEMPLATES against this file (placeholders wildcarded), so
// a builder.go / fencing.go template edit either regenerates the golden
// (visible in review + REDs the seed drift test) or fails here.
//
// The canonical config deliberately exercises every static template line the
// catalogue transcribes: staged self-awareness (Stage 3 Awakened Fox),
// evolution tier, sophistication calibration, address style, fenced learner
// preferences (chips + note), hint policy, strict citations, capability
// boundary, token budget, and the [SKILLS] weave.
//
// Regenerate with:
//
//	go test ./internal/agent/ -run FamiliarGolden -update

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/apollo-chora/chora-companion-chat/internal/skillregistry"
)

var updateGolden = flag.Bool("update", false, "regenerate testdata/golden_*.txt files")

// goldenFamiliarCfg is the canonical staged config frozen into the golden.
func goldenFamiliarCfg() *skillregistry.FamiliarConfig {
	return &skillregistry.FamiliarConfig{
		FamiliarID:     "01957c8c-1111-7000-aaaa-1111aaaa1111",
		Name:           "Newton",
		Species:        "fox",
		GrowthStage:    3,
		Specialization: "math",
		AgeStage:       "child",
		EvolutionTier:  "adept",
		PersonaSummary: "Patient algebra guide using everyday analogies.",
		GuidanceNote:   "Use space analogies and cheer me on.",
		ConfiguredRules: skillregistry.ConfiguredRules{
			Tone:               "socratic",
			HintPolicy:         skillregistry.HintPolicy{MaxHintsBeforeReveal: 3, Progression: "ladder"},
			DifficultyCap:      "intermediate",
			Language:           "en",
			CitationStrictness: "strict",
			AddressStyle:       "nickname",
			InterestChips:      []string{"rockets", "black holes"},
		},
		// ADR-249 A1a: the agent ships exactly one tool; the canonical
		// golden reflects a config granting it, so the [TASK] cite line and
		// the [SKILLS] block render the modern shape.
		AllowedSkills:      []string{"atom.cite"},
		AllowedTools:       []string{"atom.cite"},
		SkillSlotsUnlocked: 3,
		LearnerPersona:     "curious-explorer",
	}
}

func TestComposeInstruction_FamiliarGoldenByteIdentical(t *testing.T) {
	got := ComposeInstruction(goldenFamiliarCfg())
	path := filepath.Join("testdata", "golden_instruction_canonical.txt")
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s (run with -update to generate): %v", path, err)
	}
	if got != string(want) {
		t.Fatalf("ComposeInstruction drifted from %s - if intentional, regenerate with -update AND expect the ADR-197 baseline seed drift test to demand a catalogue update", path)
	}
}

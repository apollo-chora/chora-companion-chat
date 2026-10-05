package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseTurnKind(t *testing.T) {
	for raw, want := range map[string]TurnKind{"": TurnTyped, "typed": TurnTyped, " Voice ": TurnVoice, "reflect": TurnReflect} {
		if got, err := ParseTurnKind(raw); err != nil || got != want {
			t.Errorf("ParseTurnKind(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	if _, err := ParseTurnKind("sing"); err == nil || !strings.Contains(err.Error(), "unknown_turn_kind: sing") {
		t.Errorf("unknown turn kind: %v", err)
	}
}

func TestBuildReflectionPrompt_ordersShakyAndHidesIdentifiers(t *testing.T) {
	p := BuildReflectionPrompt(ReflectionInput{
		CompanionName: "Ember", GoalTitle: "Fractions", ConceptsTotal: 12, ConceptsMastered: 7,
		ShakyConcepts: []ShakyConcept{{"Comparing fractions", 0.4}, {"Equivalent fractions", 0.9}, {"", 1.0}},
		Memories:      []Memory{{"You tried 3/6 and simplified it to 1/3, then caught the slip yourself.", "2026-08-20T10:00:00Z"}, {"", "2026-08-21"}},
	})
	for _, want := range []string{"You are Ember", "THEIR GOAL: Fractions", "of the 12 concepts", "have mastered 7",
		"- Equivalent fractions\n- Comparing fractions", "- 2026-08-20: You tried 3/6", "at most 600 characters", "exactly NO_MEMORY_YET"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
	if strings.Contains(p, "0.9") || strings.Contains(p, "0.4") || strings.Contains(p, "strength:") || strings.Contains(p, "score:") {
		t.Errorf("a score must never reach the model")
	}
	empty := BuildReflectionPrompt(ReflectionInput{})
	if !strings.Contains(empty, "their Companion") || !strings.Contains(empty, "(this goal)") || !strings.Contains(empty, noShaky) || !strings.Contains(empty, noMemories) {
		t.Errorf("empty input must render the honest placeholders: %s", empty[:200])
	}
	if strings.Contains(p, "—") {
		t.Errorf("no em dashes in the prompt")
	}
}

func TestValidateSynthesis(t *testing.T) {
	text, silent, err := ValidateSynthesis("```\n\"I remember you working through equivalent fractions.\"\n```")
	if err != nil || silent || text != "I remember you working through equivalent fractions." {
		t.Errorf("unwrap: %q %v %v", text, silent, err)
	}
	if _, silent, err := ValidateSynthesis("NO_MEMORY_YET"); err != nil || !silent {
		t.Errorf("token must be an honest decline: %v %v", silent, err)
	}
	if _, _, err := ValidateSynthesis("   "); err == nil || !strings.Contains(err.Error(), "synthesis_refused") {
		t.Errorf("empty reply must be refused: %v", err)
	}
	if _, _, err := ValidateSynthesis(strings.Repeat("x", MaxSynthesisChars+1)); err == nil || !strings.Contains(err.Error(), "601 chars") {
		t.Errorf("oversize reply must be refused with the length: %v", err)
	}
}

func TestComposeVoiceInstruction(t *testing.T) {
	diag := "```json\n" + `{"edges":[{"concept_label":"Multiplication facts","summary":"7x8 and 9x6 get swapped","suggested_angles":["arrays"],"concept_key":"mult.facts","confidence":1}]}` + "\n```"
	got, err := ComposeVoiceInstruction("Ember", "en-SG", diag)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"You are Ember", "- Multiplication facts: 7x8 and 9x6 get swapped (ways in: arrays)", "in en-SG", "at most 900 characters", "never invent one"} {
		if !strings.Contains(got, want) {
			t.Errorf("voice instruction missing %q", want)
		}
	}
	if strings.Contains(got, "mult.facts") || strings.Contains(got, "confidence") {
		t.Errorf("concept keys and scores must not reach the model")
	}
	if _, err := ComposeVoiceInstruction("", "", ""); err == nil {
		t.Errorf("missing diagnosis must be an error")
	}
	none, _ := ComposeVoiceInstruction("", "", `{"edges":[]}`)
	if !strings.Contains(none, "no Growth Edges this time") {
		t.Errorf("empty edges must render the honest placeholder")
	}
}

func TestEnvelopeRender(t *testing.T) {
	out := Envelope{TurnKind: "typed", ReplyText: "hi", ModelID: "gemini-2.5-flash", PromptVersion: "v1", PromptSource: PromptSourceEmbedded}.Render()
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatal(err)
	}
	if g, ok := m["grounding"].([]any); !ok || len(g) != 0 {
		t.Errorf("grounding must render as [] not null: %s", out)
	}
	// Empty is not the only claim worth making: the ELEMENT shape is a wire
	// contract with chora-consumption (companion.TurnGrounding /
	// TurnToolCall). Arrays of strings fail ParseTurnResultJSON and kill the
	// turn, and this test was previously silent on that.
	populated := Envelope{TurnKind: "typed", ReplyText: "hi", ModelID: "m", PromptVersion: "v1", PromptSource: PromptSourceEmbedded,
		Grounding: []GroundingRef{{AtomID: "a1", RevisionID: "r1"}},
		ToolCalls: []ToolCallRef{{Name: "cite_atom"}}}.Render()
	var pm struct {
		Grounding []map[string]any `json:"grounding"`
		ToolCalls []map[string]any `json:"tool_calls"`
	}
	if err := json.Unmarshal([]byte(populated), &pm); err != nil {
		t.Fatalf("grounding/tool_calls must be OBJECT arrays: %v (%s)", err, populated)
	}
	if len(pm.Grounding) != 1 || pm.Grounding[0]["atom_id"] != "a1" {
		t.Errorf("grounding element must carry atom_id: %s", populated)
	}
	if len(pm.ToolCalls) != 1 || pm.ToolCalls[0]["name"] != "cite_atom" {
		t.Errorf("tool_calls element must carry name: %s", populated)
	}
	if _, has := m["refusal_reason"]; has {
		t.Errorf("empty refusal_reason must be omitted")
	}
	if _, has := m["nothing_to_say"]; has {
		t.Errorf("false nothing_to_say must be omitted")
	}
	r := Envelope{TurnKind: "reflect", NothingToSay: true, ModelID: "m", PromptVersion: "v1", PromptSource: PromptSourceEmbedded}.Render()
	if !strings.Contains(r, `"nothing_to_say":true`) {
		t.Errorf("decline must be visible: %s", r)
	}
}

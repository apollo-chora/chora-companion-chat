package agent

import (
	"encoding/json"
	"testing"
)

// chora-consumption parses this envelope with
// companion.TurnGrounding{atom_id,revision_id,title,snippet} and
// companion.TurnToolCall{name,summary}. The producer conforms to the shape the
// consumer declares: arrays of OBJECTS, never arrays of strings. An array of
// strings fails ParseTurnResultJSON outright and kills the turn.
func TestEnvelopeGroundingAndToolCallsRenderAsObjects(t *testing.T) {
	out := Envelope{
		TurnKind: "typed", ReplyText: "hi",
		Grounding: []GroundingRef{{AtomID: "a1", RevisionID: "r1"}},
		ToolCalls: []ToolCallRef{{Name: "cite_atom"}},
		ModelID:   "gemini-2.5-flash", PromptVersion: "v1", PromptSource: PromptSourceEmbedded,
	}.Render()

	var m struct {
		Grounding []map[string]any `json:"grounding"`
		ToolCalls []map[string]any `json:"tool_calls"`
	}
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("envelope must decode with OBJECT elements: %v (%s)", err, out)
	}
	if len(m.Grounding) != 1 || m.Grounding[0]["atom_id"] != "a1" || m.Grounding[0]["revision_id"] != "r1" {
		t.Errorf("grounding element must be an object carrying atom_id+revision_id: %s", out)
	}
	if len(m.ToolCalls) != 1 || m.ToolCalls[0]["name"] != "cite_atom" {
		t.Errorf("tool_calls element must be an object carrying name: %s", out)
	}
	// Optional provenance fields stay omitted when unknown rather than empty.
	if _, has := m.Grounding[0]["title"]; has {
		t.Errorf("unknown title must be omitted, not sent empty: %s", out)
	}
}

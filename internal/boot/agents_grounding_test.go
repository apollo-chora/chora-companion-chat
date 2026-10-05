package boot

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"testing"

	adkmodel "google.golang.org/adk/model"
	"google.golang.org/genai"
)

// partsLLM scripts whole Part slices, so a turn can drive the REAL ADK
// tool-calling loop (model emits a FunctionCall -> runner runs cite_atom ->
// FunctionResponse comes back -> model emits the reply). scriptedLLM can only
// yield text, which is precisely why the grounding harvest was never covered.
type partsLLM struct {
	turns [][]*genai.Part
	calls []*adkmodel.LLMRequest
}

func (p *partsLLM) Name() string { return "parts" }
func (p *partsLLM) GenerateContent(_ context.Context, req *adkmodel.LLMRequest, _ bool) iter.Seq2[*adkmodel.LLMResponse, error] {
	return func(yield func(*adkmodel.LLMResponse, error) bool) {
		p.calls = append(p.calls, req)
		if len(p.turns) == 0 {
			yield(nil, errors.New("partsLLM: no turn scripted"))
			return
		}
		parts := p.turns[0]
		p.turns = p.turns[1:]
		yield(&adkmodel.LLMResponse{
			Content:      &genai.Content{Role: "model", Parts: parts},
			ModelVersion: "gemini-2.5-flash-001",
		}, nil)
	}
}

type harvested struct {
	Grounding []map[string]any `json:"grounding"`
	ToolCalls []map[string]any `json:"tool_calls"`
	ReplyText string           `json:"reply_text"`
}

func driveCitationTurn(t *testing.T, atomID string) harvested {
	t.Helper()
	llm := &partsLLM{turns: [][]*genai.Part{
		{{FunctionCall: &genai.FunctionCall{ID: "call-1", Name: "cite_atom", Args: map[string]any{"atom_id": atomID}}}},
		{{Text: "Sprint planning is where the team picks the work."}},
	}}
	out, err := runTree(t, newTree(t, llm),
		stubCompanionState(map[string]any{"turn_kind": "typed", "message": "tell me about it"}),
		"tell me about it")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	var h harvested
	if err := json.Unmarshal([]byte(out), &h); err != nil {
		t.Fatalf("envelope decode (must be OBJECT arrays): %v\n%s", err, out)
	}
	return h
}

// ADR-249's validator says the atom is real -> it may be cited, with provenance.
func TestHarvestCitesWhenTheValidatorConfirmsTheAtom(t *testing.T) {
	h := driveCitationTurn(t, "atom-stub-7")

	if len(h.ToolCalls) != 1 || h.ToolCalls[0]["name"] != "cite_atom" {
		t.Fatalf("the tool call must be recorded as an object: %+v", h.ToolCalls)
	}
	if len(h.Grounding) != 1 {
		t.Fatalf("a confirmed atom must be cited; got %d refs: %+v", len(h.Grounding), h.Grounding)
	}
	if h.Grounding[0]["atom_id"] != "atom-stub-7" {
		t.Errorf("citation must name the validated atom: %+v", h.Grounding[0])
	}
	if h.Grounding[0]["revision_id"] != "rev-stub-atom-stub-7-1" {
		t.Errorf("citation must carry the revision the validator returned: %+v", h.Grounding[0])
	}
}

// ADR-249 is an ANTI-FABRICATION guard. A rejected atom must NOT be cited:
// citing a fabricated source to a learner is worse than citing nothing.
func TestHarvestRefusesToCiteAnAtomTheValidatorRejected(t *testing.T) {
	h := driveCitationTurn(t, "atom-real-42") // not in the stub corpus -> exists=false

	if len(h.ToolCalls) != 1 || h.ToolCalls[0]["name"] != "cite_atom" {
		t.Errorf("the attempt must still be recorded in tool_calls: %+v", h.ToolCalls)
	}
	if len(h.Grounding) != 0 {
		t.Errorf("a REJECTED atom must never be cited; got %+v", h.Grounding)
	}
}

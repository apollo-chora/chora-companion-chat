package tool

import (
	"context"
	"encoding/json"
	"testing"
)

// The harvest in boot/agents.go reads the atom id back off the cite_atom
// FunctionResponse. Before ADR-249's guard can be enforced, the response has
// to actually carry the id it validated.
func TestCiteAtomResponseEchoesTheValidatedAtomID(t *testing.T) {
	resp, err := CiteAtom(context.Background(), StubValidator{}, CiteAtomRequest{AtomID: "atom-stub-7"}, "t1")
	if err != nil {
		t.Fatalf("CiteAtom: %v", err)
	}
	if resp.AtomID != "atom-stub-7" {
		t.Errorf("response must echo the validated atom_id; got %q", resp.AtomID)
	}
	if !resp.Exists {
		t.Errorf("atom-stub-7 must validate under the stub corpus")
	}

	// The harvest reads a map, not the struct: assert the WIRE carries it.
	b, _ := json.Marshal(resp)
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if id, ok := m["atom_id"].(string); !ok || id != "atom-stub-7" {
		t.Errorf("FunctionResponse.Response must carry atom_id; got %v (raw %s)", m["atom_id"], b)
	}

	// A REJECTED id must still echo which id was rejected, so the refusal is
	// attributable, but must not claim existence.
	bad, err := CiteAtom(context.Background(), StubValidator{}, CiteAtomRequest{AtomID: "atom-real-42"}, "t1")
	if err != nil {
		t.Fatalf("CiteAtom(reject): %v", err)
	}
	if bad.Exists {
		t.Errorf("a non-stub id must not validate")
	}
	if bad.AtomID != "atom-real-42" {
		t.Errorf("a rejection must still name the id it rejected; got %q", bad.AtomID)
	}
}

package agent

// RED-first test for the ADR-197 M-A condition extractor for the Familiar
// companion. Familiar's prompt is composed per-instance from FamiliarConfig
// resolved via the registry; for M-A we surface the reliably-in-state
// discriminants (familiar_id, mana_tier). The content_hash still captures the
// full per-instance prompt; richer species/stage discriminants arrive with the
// M-B registry. Reuses the in-package memReadonlyState fake.

import "testing"

func TestInstanceConditions(t *testing.T) {
	st := &memReadonlyState{m: map[string]any{"familiar_id": "fam-1", "mana_tier": "basic"}}
	c := InstanceConditions(st)
	if c["familiar_id"] != "fam-1" {
		t.Errorf("familiar_id: got %q", c["familiar_id"])
	}
	if c["mana_tier"] != "basic" {
		t.Errorf("mana_tier: got %q", c["mana_tier"])
	}
}

func TestInstanceConditions_omitsMissing(t *testing.T) {
	st := &memReadonlyState{m: map[string]any{"familiar_id": "fam-2"}}
	c := InstanceConditions(st)
	if _, ok := c["mana_tier"]; ok {
		t.Error("missing mana_tier must be omitted")
	}
}

func TestInstanceConditions_nilSafe(t *testing.T) {
	if c := InstanceConditions(nil); len(c) != 0 {
		t.Errorf("nil state should yield empty map; got %v", c)
	}
}

func TestFamiliarPromptVersion_nonEmpty(t *testing.T) {
	if FamiliarPromptVersion == "" {
		t.Error("FamiliarPromptVersion must be a non-empty embedded-default version")
	}
}

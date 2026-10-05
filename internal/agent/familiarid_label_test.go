package agent

// Tests for familiar_id label injection into billing labels.
//
// TDD RED phase — CHO-1576 Phase 1 Agent A.
//
// The manaplugin already injects `tenant_id`, `user_gcid`, `crew_kind`,
// `chora_env` as billing labels. ADR-149 extends this to also stamp
// `familiar_id` so BigQuery billing exports can decompose cost per Familiar.
//
// The injection point is the AgentEngine-variant session state: the manaplugin
// reads session state for billing label construction. The familiar_id label
// is added by FamiliarBillingLabels() which the main.go manaplugin BeforeRun
// callback calls via the session state (familiar_id is already present in state
// since instancedispatch puts it there).
//
// FamiliarBillingLabels returns the canonical label set to stamp on LLM calls
// from the Familiar crew. This is tested as a pure function (no plugin context
// needed for the unit test).

import (
	"testing"
)

// ---------- FamiliarBillingLabels ----------

func TestFamiliarBillingLabels_includesFamiliarID(t *testing.T) {
	const familiarID = "01957c8c-1111-7000-aaaa-1111aaaa1111"
	const tenantID = "01957c8c-0000-7000-8888-000088880000"
	const gcid = "01957c8c-0000-7000-9999-000099990000"
	const crewKind = "familiar"

	labels := FamiliarBillingLabels(familiarID, tenantID, gcid, crewKind)

	if labels["familiar_id"] != familiarID {
		t.Errorf("want familiar_id=%q in billing labels; got %q", familiarID, labels["familiar_id"])
	}
	if labels["tenant_id"] != tenantID {
		t.Errorf("want tenant_id=%q in billing labels; got %q", tenantID, labels["tenant_id"])
	}
	if labels["crew_kind"] != crewKind {
		t.Errorf("want crew_kind=%q in billing labels; got %q", crewKind, labels["crew_kind"])
	}
}

func TestFamiliarBillingLabels_distinctForDifferentFamiliars(t *testing.T) {
	labelsA := FamiliarBillingLabels("01957c8c-1111-7000-aaaa-1111aaaa1111", "t1", "u1", "familiar")
	labelsB := FamiliarBillingLabels("01957c8c-2222-7000-bbbb-2222bbbb2222", "t1", "u1", "familiar")

	if labelsA["familiar_id"] == labelsB["familiar_id"] {
		t.Error("different familiar_ids must produce different familiar_id billing label values")
	}
}

func TestFamiliarBillingLabels_keysAreLowercaseSnakeCase(t *testing.T) {
	labels := FamiliarBillingLabels("fid", "tid", "uid", "familiar")
	for k := range labels {
		for _, ch := range k {
			if ch >= 'A' && ch <= 'Z' {
				t.Errorf("billing label key %q must be lowercase; found uppercase char %c", k, ch)
			}
		}
	}
}

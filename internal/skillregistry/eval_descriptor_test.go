package skillregistry

import (
	"os"
	"strings"
	"testing"

	consumptionv1 "github.com/apollo-chora/chora-contracts/gen/go/chora/services/consumption/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"gopkg.in/yaml.v3"
)

// Verifies the eval descriptor's seeded familiar_config is a VALID protojson
// CompanionInstanceConfig and decodes to the canonical golden identity.
// protojson.Unmarshal rejects unknown fields, so a typo here would fail the
// gate at run time with an opaque resolver error. The descriptor's
// session_state_base block is carried as testdata (the descriptor itself
// lived in the legacy eval lane, outside this repo).
func TestEvalDescriptorFamiliarConfigParses(t *testing.T) {
	raw, err := os.ReadFile("testdata/eval_descriptor_session_state.yaml")
	if err != nil {
		t.Fatalf("descriptor not reachable (a skip here would be a false pass): %v", err)
	}
	var d struct {
		SessionStateBase map[string]string `yaml:"session_state_base"`
	}
	if err := yaml.Unmarshal(raw, &d); err != nil {
		t.Fatalf("yaml: %v", err)
	}
	cfgJSON := d.SessionStateBase["familiar_config"]
	if strings.TrimSpace(cfgJSON) == "" {
		t.Fatal("descriptor seeds no familiar_config")
	}
	var pc consumptionv1.CompanionInstanceConfig
	if err := protojson.Unmarshal([]byte(cfgJSON), &pc); err != nil {
		t.Fatalf("protojson.Unmarshal REJECTED the seeded config: %v", err)
	}
	got := protoToFamiliarConfig(&pc)
	if got.FamiliarID != d.SessionStateBase["familiar_id"] {
		t.Errorf("familiar_id %q != familiar_config.familiarId %q; dispatch would resolve a different instance",
			d.SessionStateBase["familiar_id"], got.FamiliarID)
	}
	if got.GrowthStage != 3 {
		t.Errorf("growth_stage = %d, want 3 (the stage the persona goldens encode)", got.GrowthStage)
	}
	if got.Specialization != "math" || got.AgeStage != "child" {
		t.Errorf("specialization/age_stage = %q/%q, want math/child", got.Specialization, got.AgeStage)
	}
	if got.ConfiguredRules.DifficultyCap != "intermediate" || got.ConfiguredRules.HintPolicy.MaxHintsBeforeReveal != 3 {
		t.Errorf("rules drifted from the golden: cap=%q hints=%d",
			got.ConfiguredRules.DifficultyCap, got.ConfiguredRules.HintPolicy.MaxHintsBeforeReveal)
	}
	if got.TenantID != d.SessionStateBase["tenant_id"] {
		t.Errorf("tenant mismatch %q vs %q", got.TenantID, d.SessionStateBase["tenant_id"])
	}
	t.Logf("OK: %s/%s stage=%d tier=%s tools=%v", got.Name, got.Specialization, got.GrowthStage, got.EvolutionTier, got.AllowedTools)
}

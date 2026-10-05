// state_registry_test.go — StateRegistry (config from session state; the
// bug-3 mesh-sidestep). Ported unchanged from the deleted grpc_registry_test.go
// when ADR-249 A1a removed the dead GRPCRegistry.
package skillregistry

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/apollo-chora/chora-adk-common/instancedispatch"
	consumptionv1 "github.com/apollo-chora/chora-contracts/gen/go/chora/services/consumption/v1"
)

func TestStateRegistry_ReadsConfigFromCtx(t *testing.T) {
	pc := &consumptionv1.CompanionInstanceConfig{
		CompanionId: "fam-1", Name: "Eira", Specialization: "cspo",
		EvolutionTier: "apprentice", GrowthStage: 2, AllowedSkills: []string{"atom.search"},
		ConfiguredRules: &consumptionv1.CompanionConfiguredRules{Tone: "socratic", MaxHintsBeforeReveal: 3},
	}
	b, err := protojson.Marshal(pc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	ctx := instancedispatch.WithFamiliarConfigJSON(context.Background(), string(b))
	cfg, err := NewStateRegistry().LoadFamiliarConfig(ctx, "fam-1")
	if err != nil {
		t.Fatalf("LoadFamiliarConfig: %v", err)
	}
	if cfg.Name != "Eira" || cfg.Specialization != "cspo" || cfg.GrowthStage != 2 ||
		cfg.EvolutionTier != "apprentice" || len(cfg.AllowedSkills) != 1 ||
		cfg.ConfiguredRules.Tone != "socratic" || cfg.ConfiguredRules.HintPolicy.MaxHintsBeforeReveal != 3 {
		t.Errorf("state config mapping wrong: %+v", cfg)
	}
}

func TestStateRegistry_MissingConfigIsWiringError(t *testing.T) {
	// No familiar_config stamped onto ctx → wiring error, NOT ErrFamiliarNotFound.
	_, err := NewStateRegistry().LoadFamiliarConfig(context.Background(), "fam-1")
	if err == nil {
		t.Fatal("want error when familiar_config absent from ctx")
	}
	if errors.Is(err, ErrFamiliarNotFound) {
		t.Fatal("missing config must be a wiring error, not ErrFamiliarNotFound")
	}
}

func TestStateRegistry_EmptyFamiliarID(t *testing.T) {
	if _, err := NewStateRegistry().LoadFamiliarConfig(context.Background(), ""); !errors.Is(err, ErrFamiliarNotFound) {
		t.Fatalf("empty familiar_id → ErrFamiliarNotFound, got %v", err)
	}
}

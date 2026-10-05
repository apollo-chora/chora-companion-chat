package agent

// Pre-hatch guard tests — Stage 0 (Mystery Egg / unbound) Familiar refusal.
//
// TDD RED phase — CHO-1576 Phase 1 Agent A.
//
// Per ADR-149 + the S5 audit (§7 "Agent A REVISED"):
//   - A Familiar with GrowthStage == 0 (Mystery Egg, unbound, pre-hatch) MUST
//     be refused by the instancedispatch BeforeRunCallback path.
//   - The refusal reason is `familiar_pre_hatch`.
//   - This is enforced at the resolver level: NewFamiliarResolver returns an
//     error from the resolver closure when cfg.GrowthStage == 0.
//
// The resolver-level check is the correct enforcement point because:
//   - The resolver is called by both BeforeRunCallback (turn refusal) and
//     BeforeModelCallback (tool filter) via ResolveInstanceFromState.
//   - A resolver that errors = BeforeRunCallback returns error = turn refused
//     before any LLM call is made.
//   - This is consistent with how the resolver handles "familiar not found" and
//     "config invariant violation" — all are resolver errors that propagate.
//
// The new exported helper RefusePreHatch(cfg) is invoked from the resolver
// closure (in dispatch.go) before returning the InstanceConfig.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/apollo-chora/chora-companion-chat/internal/skillregistry"
)

// ---------- RefusePreHatch ----------

func TestRefusePreHatch_returnsErrPreHatchForStage0(t *testing.T) {
	egg := &skillregistry.FamiliarConfig{
		FamiliarID:     "01957c8c-aaa2-7000-ffff-aaaa0000ffff",
		Name:           "Mystery Egg",
		Specialization: "unbound",
		GrowthStage:    0,
		Species:        "dragon",
	}
	err := RefusePreHatch(egg)
	if err == nil {
		t.Fatal("Stage 0 (unbound) must return an error from RefusePreHatch")
	}
	if !errors.Is(err, ErrFamiliarPreHatch) {
		t.Errorf("want ErrFamiliarPreHatch; got %v", err)
	}
	// The error message must contain the familiar_pre_hatch signal so
	// terminationplugin observers can classify the refusal reason.
	if !strings.Contains(err.Error(), "familiar_pre_hatch") {
		t.Errorf("error must mention familiar_pre_hatch; got %q", err.Error())
	}
}

func TestRefusePreHatch_returnsNilForStage1(t *testing.T) {
	baby := &skillregistry.FamiliarConfig{
		FamiliarID:     "01957c8c-1111-7000-aaaa-1111aaaa1111",
		Name:           "Eira",
		Specialization: "cspo",
		GrowthStage:    1,
		Species:        "dragon",
	}
	if err := RefusePreHatch(baby); err != nil {
		t.Errorf("Stage 1 (Baby) must not be refused; got %v", err)
	}
}

func TestRefusePreHatch_returnsNilForAllHatchedStages(t *testing.T) {
	for stage := 1; stage <= 6; stage++ {
		cfg := &skillregistry.FamiliarConfig{
			GrowthStage: stage,
			Species:     "owl",
		}
		if err := RefusePreHatch(cfg); err != nil {
			t.Errorf("Stage %d should NOT be refused; got %v", stage, err)
		}
	}
}

func TestRefusePreHatch_returnsNilForNilCfg(t *testing.T) {
	// nil cfg is caught by ValidateConfigInvariants upstream; RefusePreHatch
	// should not crash on nil — it is not the nil-check responsibility.
	// (Behaviour: nil cfg has no GrowthStage field, so it's implicitly 0 —
	// but we choose to leave nil validation to ValidateConfigInvariants and
	// let RefusePreHatch only check the growth_stage field.)
	// Contract: nil cfg → no error from RefusePreHatch (it is not a stage-0 explicit cfg).
	if err := RefusePreHatch(nil); err != nil {
		t.Errorf("nil cfg must not trigger pre-hatch refusal; got %v", err)
	}
}

// ---------- NewFamiliarResolver integration — pre-hatch refusal propagated ----------

func TestFamiliarResolver_refusesPreHatchFamiliarFromState(t *testing.T) {
	// Inject a stub registry that returns a Stage-0 Familiar config when
	// the given familiar_id is queried. The resolver must propagate
	// ErrFamiliarPreHatch so instancedispatch.BeforeRunCallback refuses.
	egg := &skillregistry.FamiliarConfig{
		FamiliarID:     "01957c8c-aaa2-7000-ffff-aaaa0000ffff",
		Name:           "Mystery Egg",
		Specialization: "unbound",
		GrowthStage:    0,
		Species:        "dragon",
		// GrowthStage=0, SkillSlotsUnlocked must be ≥0 for ValidateConfigInvariants.
		AllowedSkills:      []string{},
		SkillSlotsUnlocked: 0,
	}
	reg := &staticRegistry{cfg: egg}
	avail := newStubAvailable("atom.search")
	resolver, err := NewFamiliarResolver(reg, avail)
	if err != nil {
		t.Fatalf("NewFamiliarResolver: %v", err)
	}

	_, resolveErr := resolver(context.Background(), egg.FamiliarID)
	if resolveErr == nil {
		t.Fatal("resolver must refuse Stage-0 (unbound) Familiar; got nil error")
	}
	if !errors.Is(resolveErr, ErrFamiliarPreHatch) {
		t.Errorf("want ErrFamiliarPreHatch in resolver error chain; got %v", resolveErr)
	}
}

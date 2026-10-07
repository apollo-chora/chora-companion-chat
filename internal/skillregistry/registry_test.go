package skillregistry

import (
	"context"
	"errors"
	"testing"
)

func TestStubRegistry_loadsMathFamiliar(t *testing.T) {
	r := NewStubRegistry()
	cfg, err := r.LoadFamiliarConfig(context.Background(), stubMathFamiliarID)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if cfg.Specialization != "math" {
		t.Errorf("want specialization=math; got %q", cfg.Specialization)
	}
	if cfg.AgeStage != "child" {
		t.Errorf("want age_stage=child; got %q", cfg.AgeStage)
	}
	if cfg.EvolutionTier != "adept" {
		t.Errorf("want evolution_tier=adept; got %q", cfg.EvolutionTier)
	}
	if cfg.SkillSlotsUnlocked != 3 {
		t.Errorf("want 3 skill slots; got %d", cfg.SkillSlotsUnlocked)
	}
	if cfg.ConfiguredRules.Tone != "socratic" {
		t.Errorf("want tone=socratic; got %q", cfg.ConfiguredRules.Tone)
	}
	// ADR-249 A1a: the agent ships exactly one tool, so the stub allowlist
	// carries exactly atom.cite.
	if len(cfg.AllowedSkills) != 1 || cfg.AllowedSkills[0] != "atom.cite" {
		t.Errorf("want allowed skills [atom.cite]; got %v", cfg.AllowedSkills)
	}
}

func TestStubRegistry_returnsDistinctConfigsPerFamiliar(t *testing.T) {
	r := NewStubRegistry()

	math, err := r.LoadFamiliarConfig(context.Background(), stubMathFamiliarID)
	if err != nil {
		t.Fatal(err)
	}
	history, err := r.LoadFamiliarConfig(context.Background(), stubHistoryFamiliarID)
	if err != nil {
		t.Fatal(err)
	}
	coding, err := r.LoadFamiliarConfig(context.Background(), stubCodingFamiliarID)
	if err != nil {
		t.Fatal(err)
	}

	if math.Specialization == history.Specialization {
		t.Errorf("math/history specializations should differ; both %q", math.Specialization)
	}
	if history.Specialization == coding.Specialization {
		t.Errorf("history/coding specializations should differ; both %q", history.Specialization)
	}
	if math.EvolutionTier == history.EvolutionTier {
		t.Errorf("math/history tier should differ to exercise tier paths; both %q", math.EvolutionTier)
	}

	// History Familiar is young_forever; math is not. This isolates the toggle.
	if !history.YoungForever {
		t.Error("history Familiar must be young_forever for test coverage")
	}
	if math.YoungForever {
		t.Error("math Familiar must NOT be young_forever for test coverage")
	}
}

func TestStubRegistry_returnsErrorForUnknownFamiliar(t *testing.T) {
	r := NewStubRegistry()
	_, err := r.LoadFamiliarConfig(context.Background(), "01999999-9999-7000-9999-999999999999")
	if !errors.Is(err, ErrFamiliarNotFound) {
		t.Errorf("want ErrFamiliarNotFound; got %v", err)
	}
}

func TestStubRegistry_returnsErrorForEmptyID(t *testing.T) {
	r := NewStubRegistry()
	_, err := r.LoadFamiliarConfig(context.Background(), "")
	if !errors.Is(err, ErrFamiliarNotFound) {
		t.Errorf("want ErrFamiliarNotFound for empty id; got %v", err)
	}
}

func TestStubRegistry_returnsDefensiveCopy(t *testing.T) {
	r := NewStubRegistry()
	c1, err := r.LoadFamiliarConfig(context.Background(), stubMathFamiliarID)
	if err != nil {
		t.Fatal(err)
	}

	// Caller mutates a slice on the copy.
	c1.AllowedSkills = append(c1.AllowedSkills, "math.solver")
	c1.Specialization = "tampered"

	c2, err := r.LoadFamiliarConfig(context.Background(), stubMathFamiliarID)
	if err != nil {
		t.Fatal(err)
	}
	if c2.Specialization != "math" {
		t.Errorf("registry config mutated by caller; got %q (defensive copy broken)", c2.Specialization)
	}
	if len(c2.AllowedSkills) != 1 {
		t.Errorf("AllowedSkills slice should be copied; got len=%d", len(c2.AllowedSkills))
	}
}

// ---------- LearnerPersona overlay (ADR-116 Amendment 2, Iter 1 BLANKET) ----------

func TestStubRegistry_carriesLearnerPersonaPerFamiliar(t *testing.T) {
	// Per ADR-116 Amendment 2: each stub Familiar carries one learner_persona
	// for cross-tuple fixture coverage at the matrix level. The 3-Familiar
	// stub set spans all 3 persona values exactly once.
	r := NewStubRegistry()

	math, err := r.LoadFamiliarConfig(context.Background(), stubMathFamiliarID)
	if err != nil {
		t.Fatal(err)
	}
	history, err := r.LoadFamiliarConfig(context.Background(), stubHistoryFamiliarID)
	if err != nil {
		t.Fatal(err)
	}
	coding, err := r.LoadFamiliarConfig(context.Background(), stubCodingFamiliarID)
	if err != nil {
		t.Fatal(err)
	}

	if math.LearnerPersona == "" {
		t.Error("math Familiar must carry a LearnerPersona (defaults disallowed at stub layer)")
	}
	if history.LearnerPersona == "" {
		t.Error("history Familiar must carry a LearnerPersona")
	}
	if coding.LearnerPersona == "" {
		t.Error("coding Familiar must carry a LearnerPersona")
	}

	// 3 distinct values — exercises the full 3×3 fixture matrix at smoke time.
	personas := map[string]bool{
		math.LearnerPersona:    true,
		history.LearnerPersona: true,
		coding.LearnerPersona:  true,
	}
	if len(personas) != 3 {
		t.Errorf("stub Familiars must span 3 distinct learner_persona values; got %v", personas)
	}

	// Each persona must be one of the three canonical ADR-116 Amendment 2 values.
	canonical := map[string]struct{}{
		"curious-explorer": {},
		"cert-focused":     {},
		"social-leader":    {},
	}
	for _, c := range []*FamiliarConfig{math, history, coding} {
		if _, ok := canonical[c.LearnerPersona]; !ok {
			t.Errorf("Familiar %s has non-canonical LearnerPersona %q (want curious-explorer|cert-focused|social-leader)",
				c.FamiliarID, c.LearnerPersona)
		}
	}
}

func TestStubRegistry_learnerPersonaRoundTripsThroughDefensiveCopy(t *testing.T) {
	// The defensive-copy path in LoadFamiliarConfig must preserve LearnerPersona
	// (regression guard — adding a field requires extending defensive copy).
	r := NewStubRegistry()
	c1, err := r.LoadFamiliarConfig(context.Background(), stubMathFamiliarID)
	if err != nil {
		t.Fatal(err)
	}
	original := c1.LearnerPersona
	c1.LearnerPersona = "tampered"
	c2, err := r.LoadFamiliarConfig(context.Background(), stubMathFamiliarID)
	if err != nil {
		t.Fatal(err)
	}
	if c2.LearnerPersona != original {
		t.Errorf("LearnerPersona mutated through caller's copy; got %q, want %q (defensive copy broken)",
			c2.LearnerPersona, original)
	}
}

// ---------- ADR-149 Growth-Stage axis (Iter G.4) ----------

func TestStubRegistry_carriesGrowthStageFields(t *testing.T) {
	// Each stub Familiar must carry the ADR-149 growth-stage fields
	// (GrowthStage, Species, ResonantAtomID, VisibleKgNeighborAtomIDs).
	// Plan §G.4: Newton=Stage 2 Owl, Curie=Stage 4 Fox, Lovelace=Stage 6 Dragon.
	r := NewStubRegistry()

	math, err := r.LoadFamiliarConfig(context.Background(), stubMathFamiliarID)
	if err != nil {
		t.Fatal(err)
	}
	if math.GrowthStage != 2 {
		t.Errorf("Newton: want GrowthStage=2 (Fledgling); got %d", math.GrowthStage)
	}
	if math.Species != "owl" {
		t.Errorf("Newton: want Species=owl; got %q", math.Species)
	}
	if math.ResonantAtomID == "" {
		t.Error("Newton: ResonantAtomID must be non-empty (mandatory per ADR-149)")
	}

	history, err := r.LoadFamiliarConfig(context.Background(), stubHistoryFamiliarID)
	if err != nil {
		t.Fatal(err)
	}
	if history.GrowthStage != 4 {
		t.Errorf("Curie: want GrowthStage=4 (Structural); got %d", history.GrowthStage)
	}
	if history.Species != "fox" {
		t.Errorf("Curie: want Species=fox; got %q", history.Species)
	}

	coding, err := r.LoadFamiliarConfig(context.Background(), stubCodingFamiliarID)
	if err != nil {
		t.Fatal(err)
	}
	if coding.GrowthStage != 6 {
		t.Errorf("Lovelace: want GrowthStage=6 (Matured); got %d", coding.GrowthStage)
	}
	if coding.Species != "dragon" {
		t.Errorf("Lovelace: want Species=dragon; got %q", coding.Species)
	}
	if !coding.ShinyVariant {
		t.Error("Lovelace: ShinyVariant should be true (duplicate-roll marker)")
	}
}

func TestStubRegistry_stagesSpanEarlyAndLateTiers(t *testing.T) {
	// G.4 spec: stub Familiars must span the early-tier (Stages 1-3) and
	// late-tier (Stages 4-6) few-shot banks for exhaustive smoke coverage.
	r := NewStubRegistry()

	math, _ := r.LoadFamiliarConfig(context.Background(), stubMathFamiliarID)
	history, _ := r.LoadFamiliarConfig(context.Background(), stubHistoryFamiliarID)
	coding, _ := r.LoadFamiliarConfig(context.Background(), stubCodingFamiliarID)

	stages := []int{math.GrowthStage, history.GrowthStage, coding.GrowthStage}

	hasEarly := false
	hasLate := false
	for _, s := range stages {
		if s >= 1 && s <= 3 {
			hasEarly = true
		}
		if s >= 4 && s <= 6 {
			hasLate = true
		}
	}
	if !hasEarly {
		t.Errorf("stub Familiars must include at least one early-tier (Stages 1-3) Familiar; got %v", stages)
	}
	if !hasLate {
		t.Errorf("stub Familiars must include at least one late-tier (Stages 4-6) Familiar; got %v", stages)
	}
}

func TestStubRegistry_speciesAreCanonical(t *testing.T) {
	// Per ADR-149 §"Breed lootbox" the canonical species enum is
	// {owl, fox, cat, dragon, phoenix, turtle, wolf, raven}. The stub
	// Familiars must each carry a value from this enum.
	canonical := map[string]struct{}{
		"owl": {}, "fox": {}, "cat": {}, "dragon": {},
		"phoenix": {}, "turtle": {}, "wolf": {}, "raven": {},
	}
	r := NewStubRegistry()
	for _, id := range []string{stubMathFamiliarID, stubHistoryFamiliarID, stubCodingFamiliarID} {
		cfg, err := r.LoadFamiliarConfig(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := canonical[cfg.Species]; !ok {
			t.Errorf("familiar %s species %q not in canonical enum",
				cfg.FamiliarID, cfg.Species)
		}
	}
}

func TestStubRegistry_visibleKgNeighborsCopiedDefensively(t *testing.T) {
	// Caller mutation of VisibleKgNeighborAtomIDs must NOT leak into the
	// registry's stored config.
	r := NewStubRegistry()
	c1, err := r.LoadFamiliarConfig(context.Background(), stubCodingFamiliarID)
	if err != nil {
		t.Fatal(err)
	}
	originalLen := len(c1.VisibleKgNeighborAtomIDs)
	c1.VisibleKgNeighborAtomIDs = append(c1.VisibleKgNeighborAtomIDs, "tampered-atom")

	c2, err := r.LoadFamiliarConfig(context.Background(), stubCodingFamiliarID)
	if err != nil {
		t.Fatal(err)
	}
	if len(c2.VisibleKgNeighborAtomIDs) != originalLen {
		t.Errorf("VisibleKgNeighborAtomIDs leaked; got len=%d want %d",
			len(c2.VisibleKgNeighborAtomIDs), originalLen)
	}
}

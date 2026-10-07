package agent

import (
	"context"
	"iter"
	"strings"
	"testing"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
)

// nopInstructionProvider is a minimal InstructionProvider used by builder tests
// that only exercise validation paths (never actually invoked).
func nopInstructionProvider(ctx agent.ReadonlyContext) (string, error) { return "", nil }

// stubModel satisfies model.LLM for build-time validation tests. Never called
// in tests — only exists so we can pass a non-nil model to BuildFamiliarAgent.
type stubModel struct{}

func (stubModel) Name() string { return "stub-model" }
func (stubModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {}
}

// Compile-time check: stubModel satisfies model.LLM.
var _ model.LLM = stubModel{}

// stubTool is a minimal tool.Tool stand-in shared by the dispatch-resolver,
// dispatch-override and prehatch tests. The agent runtime never invokes
// these stubs in unit tests.
type stubTool struct{ name string }

func (s stubTool) Name() string        { return s.name }
func (s stubTool) Description() string { return "" }
func (s stubTool) IsLongRunning() bool { return false }

func newStubAvailable(keys ...string) map[string]tool.Tool {
	out := make(map[string]tool.Tool, len(keys))
	for _, k := range keys {
		out[k] = stubTool{name: k}
	}
	return out
}

// ---------- BuildFamiliarAgent ----------

func TestBuildFamiliarAgent_rejectsNilModel(t *testing.T) {
	a, err := BuildFamiliarAgent(context.Background(), nil, nopInstructionProvider, nil)
	if err == nil {
		t.Fatal("want error for nil model; got nil")
	}
	if a != nil {
		t.Error("agent must be nil when build fails")
	}
}

func TestBuildFamiliarAgent_rejectsNilInstructionProvider(t *testing.T) {
	// Even with a valid model surface, missing provider must be rejected —
	// the dispatch-plugin variant relies on per-turn composition for the system
	// prompt; a static-only agent silently degrades multi-Familiar dispatch.
	a, err := BuildFamiliarAgent(context.TODO(), stubModel{}, nil, nil)
	if err == nil {
		t.Fatal("want error for nil InstructionProvider; got nil")
	}
	if a != nil {
		t.Error("agent must be nil when build fails")
	}
}

// ---------- FewShots fixture loader ----------
//
// Few-shot fixtures live at internal/agent/few_shots/<spec>_<persona>.yaml.
// Loaded once at process start (deterministic prompt → IMDA D2 reproducibility).

func TestFewShots_loads9CanonicalFixtures(t *testing.T) {
	// Per plan §B.6 (CREATE refactor + few-shot fixture matrix):
	// 3 specialisations × 3 personas = 9 fixtures must load at process start.
	store, err := LoadFewShots()
	if err != nil {
		t.Fatalf("LoadFewShots failed: %v", err)
	}
	for _, spec := range []string{"math", "history", "coding"} {
		for _, persona := range []string{"curious-explorer", "cert-focused", "social-leader"} {
			ex, ok := store.LookupForStage(spec, persona, StageTierEarly)
			if !ok {
				t.Errorf("fixture (%s, %s) missing", spec, persona)
				continue
			}
			if len(ex) != 3 {
				t.Errorf("(%s, %s): want 3 examples; got %d", spec, persona, len(ex))
			}
			// Each example must carry at least a user prompt + assistant reply.
			for i, e := range ex {
				if strings.TrimSpace(e.UserPrompt) == "" {
					t.Errorf("(%s, %s) example %d: UserPrompt empty", spec, persona, i)
				}
				if strings.TrimSpace(e.AssistantReply) == "" {
					t.Errorf("(%s, %s) example %d: AssistantReply empty", spec, persona, i)
				}
			}
		}
	}
}

func TestFewShots_lookupUnknownTupleFallsBackOrErrors(t *testing.T) {
	// Unknown specialisation/persona must NOT silently return nil examples —
	// the caller (ComposeInstruction) treats missing fixtures as falling back
	// to a known-good default rather than producing a malformed prompt.
	store, err := LoadFewShots()
	if err != nil {
		t.Fatalf("LoadFewShots failed: %v", err)
	}
	ex, ok := store.LookupForStage("alchemy", "curious-explorer", StageTierEarly)
	// Either contractual: returns (empty, false) so caller can fall back —
	// the documented contract. NOT a panic, NOT a malformed slice.
	if ok && len(ex) == 0 {
		t.Error("LookupForStage must return (nil/empty, false) on unknown tuple — not (empty, true)")
	}
}

// ---------- ADR-149 stage tier banks (Iter G.4) ----------

func TestFewShots_loads18FixturesAcrossBothBanks(t *testing.T) {
	// G.4 spec: 9 tuples × 2 stage tiers (early + late) = 18 fixtures must load.
	store, err := LoadFewShots()
	if err != nil {
		t.Fatalf("LoadFewShots failed: %v", err)
	}
	for _, spec := range []string{"math", "history", "coding"} {
		for _, persona := range []string{"curious-explorer", "cert-focused", "social-leader"} {
			for _, tier := range []StageTier{StageTierEarly, StageTierLate} {
				ex, ok := store.LookupForStage(spec, persona, tier)
				if !ok {
					t.Errorf("fixture (%s, %s, %s) missing", spec, persona, tier)
					continue
				}
				if len(ex) != 3 {
					t.Errorf("(%s, %s, %s): want 3 examples; got %d", spec, persona, tier, len(ex))
				}
				for i, e := range ex {
					if strings.TrimSpace(e.UserPrompt) == "" {
						t.Errorf("(%s, %s, %s) example %d: UserPrompt empty",
							spec, persona, tier, i)
					}
					if strings.TrimSpace(e.AssistantReply) == "" {
						t.Errorf("(%s, %s, %s) example %d: AssistantReply empty",
							spec, persona, tier, i)
					}
				}
			}
		}
	}
}

func TestFewShots_lookupForGrowthStageRoutesCorrectBank(t *testing.T) {
	store, err := LoadFewShots()
	if err != nil {
		t.Fatalf("LoadFewShots failed: %v", err)
	}
	// Stages 1-3 → early bank; Stages 4-6 → late bank.
	early, eOk := store.LookupForStage("math", "curious-explorer", StageTierEarly)
	late, lOk := store.LookupForStage("math", "curious-explorer", StageTierLate)
	if !eOk || !lOk {
		t.Fatal("both banks must load")
	}
	for _, s := range []int{1, 2, 3} {
		got, ok := store.LookupForGrowthStage("math", "curious-explorer", s)
		if !ok {
			t.Errorf("stage %d should resolve via early bank", s)
			continue
		}
		if !sameExamples(got, early) {
			t.Errorf("stage %d should route to early bank", s)
		}
	}
	for _, s := range []int{4, 5, 6} {
		got, ok := store.LookupForGrowthStage("math", "curious-explorer", s)
		if !ok {
			t.Errorf("stage %d should resolve via late bank", s)
			continue
		}
		if !sameExamples(got, late) {
			t.Errorf("stage %d should route to late bank", s)
		}
	}
	if _, ok := store.LookupForGrowthStage("math", "curious-explorer", 0); ok {
		t.Error("Stage 0 (Egg) must return ok=false (no few-shots while pre-hatch)")
	}
	if _, ok := store.LookupForGrowthStage("math", "curious-explorer", 7); ok {
		t.Error("out-of-range stage 7 must return ok=false")
	}
}

func sameExamples(a, b []FewShotExample) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestCanonicaliseLearnerPersonaCases(t *testing.T) {
	// Hits every branch of canonicaliseLearnerPersona for coverage + future
	// regression guard if someone adds a new persona without updating it.
	cases := []struct {
		in, want string
	}{
		{"curious-explorer", "curious-explorer"},
		{"cert-focused", "cert-focused"},
		{"social-leader", "social-leader"},
		{"", "curious-explorer"},
		{"  curious-explorer  ", "curious-explorer"},
		{"CURIOUS-EXPLORER", "curious-explorer"},
		{"Unknown-Value", "curious-explorer"}, // defaults
	}
	for _, tc := range cases {
		got := canonicaliseLearnerPersona(tc.in)
		if got != tc.want {
			t.Errorf("canonicaliseLearnerPersona(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}

// ---------- builder private helpers — edge case coverage ----------

func TestGrowthStageName_allCases(t *testing.T) {
	cases := []struct {
		stage int
		want  string
	}{
		{0, "Egg"},
		{1, "Baby"},
		{2, "Fledgling"},
		{3, "Awakened"},
		{4, "Structural-Growth"},
		{5, "Teen"},
		{6, "Matured"},
		{7, ""},  // out-of-range
		{-1, ""}, // negative
	}
	for _, tc := range cases {
		got := growthStageName(tc.stage)
		if got != tc.want {
			t.Errorf("growthStageName(%d) = %q; want %q", tc.stage, got, tc.want)
		}
	}
}

func TestBreedAdjective_canonicalSpecies(t *testing.T) {
	cases := []struct{ species, want string }{
		{"owl", " Owl"},
		{"dragon", " Dragon"},
		{"phoenix", " Phoenix"},
		{"fox", " Fox"},
		{"", ""},                // empty → empty
		{"unicorn", " unicorn"}, // unknown species → passthrough with leading space
	}
	for _, tc := range cases {
		got := breedAdjective(tc.species)
		if got != tc.want {
			t.Errorf("breedAdjective(%q) = %q; want %q", tc.species, got, tc.want)
		}
	}
}

func TestGrowthStageMandate_coveredStages(t *testing.T) {
	// Exercise every case in the switch including default.
	for _, stage := range []int{0, 1, 2, 3, 4, 5, 6, 99} {
		got := growthStageMandate(stage)
		if got == "" {
			t.Errorf("growthStageMandate(%d) returned empty string", stage)
		}
	}
}

func TestGrowthStageSophistication_coveredStages(t *testing.T) {
	for _, stage := range []int{1, 2, 3, 4, 5, 6, 99} {
		got := growthStageSophistication(stage)
		if got == "" {
			t.Errorf("growthStageSophistication(%d) returned empty string", stage)
		}
	}
}

func TestStageOutputCap_allStagedValues(t *testing.T) {
	expected := map[int]int{0: 100, 1: 600, 2: 800, 3: 1000, 4: 1200, 5: 1400, 6: 2000}
	for stage, wantCap := range expected {
		got := stageOutputCap(stage)
		if got != wantCap {
			t.Errorf("stageOutputCap(%d) = %d; want %d", stage, got, wantCap)
		}
	}
	// out-of-range → 0
	if got := stageOutputCap(99); got != 0 {
		t.Errorf("stageOutputCap(99) = %d; want 0", got)
	}
}

func TestSafe_returnsValueWhenNonEmpty(t *testing.T) {
	if got := safe("hello", "fallback"); got != "hello" {
		t.Errorf("safe(hello, fallback) = %q; want %q", got, "hello")
	}
}

func TestSafe_returnsFallbackWhenEmpty(t *testing.T) {
	if got := safe("", "fallback"); got != "fallback" {
		t.Errorf("safe('', fallback) = %q; want %q", got, "fallback")
	}
}

func TestSafe_returnsFallbackForWhitespaceOnly(t *testing.T) {
	if got := safe("   ", "fallback"); got != "fallback" {
		t.Errorf("safe('   ', fallback) = %q; want %q", got, "fallback")
	}
}

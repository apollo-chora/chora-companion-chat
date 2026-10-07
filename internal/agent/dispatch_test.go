package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"google.golang.org/adk/tool"

	"github.com/apollo-chora/chora-adk-common/instancedispatch"
	"github.com/apollo-chora/chora-companion-chat/internal/skillregistry"
)

// ---------- NewFamiliarResolver ----------

func TestNewFamiliarResolver_rejectsNilRegistry(t *testing.T) {
	avail := newStubAvailable("atom.search")
	_, err := NewFamiliarResolver(nil, avail)
	if err == nil {
		t.Fatal("want error for nil registry; got nil")
	}
}

func TestNewFamiliarResolver_rejectsNilAvailableMap(t *testing.T) {
	reg := skillregistry.NewStubRegistry()
	_, err := NewFamiliarResolver(reg, nil)
	if err == nil {
		t.Fatal("want error for nil available map; got nil")
	}
}

func TestNewFamiliarResolver_returnsCallableResolver(t *testing.T) {
	reg := skillregistry.NewStubRegistry()
	avail := newStubAvailable("atom.cite")
	resolver, err := NewFamiliarResolver(reg, avail)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if resolver == nil {
		t.Fatal("resolver must not be nil")
	}
}

func TestFamiliarResolver_resolvesMathFamiliarConfig(t *testing.T) {
	reg := skillregistry.NewStubRegistry()
	avail := newStubAvailable("atom.cite")
	resolver, _ := NewFamiliarResolver(reg, avail)

	out, err := resolver(context.Background(), "01957c8c-1111-7000-aaaa-1111aaaa1111")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}

	// Instruction should reflect math Familiar (Newton, socratic, child)
	if !strings.Contains(out.Instruction, "Newton") {
		t.Errorf("math resolver should embed Familiar name; got %q", out.Instruction)
	}
	if !strings.Contains(out.Instruction, "math Familiar") {
		t.Errorf("math resolver should embed specialization; got %q", out.Instruction)
	}
	if !strings.Contains(out.Instruction, "socratic") {
		t.Errorf("math resolver should carry socratic tone; got %q", out.Instruction)
	}

	// AllowedTools should be the actual tool.Name() values (NOT raw skill_keys —
	// req.Tools / req.Config.Tools[].FunctionDeclarations[].Name use tool.Name()).
	// ADR-249 A1a: the build ships exactly one tool.
	wantTools := map[string]bool{"atom.cite": true}
	if len(out.AllowedTools) != 1 {
		t.Fatalf("math grants 1 tool; got %d allowed tools", len(out.AllowedTools))
	}
	for _, n := range out.AllowedTools {
		if !wantTools[n] {
			t.Errorf("unexpected tool name in allowed list: %q", n)
		}
	}
}

func TestFamiliarResolver_resolvesHistoryFamiliarConfig(t *testing.T) {
	reg := skillregistry.NewStubRegistry()
	avail := newStubAvailable("atom.cite")
	resolver, _ := NewFamiliarResolver(reg, avail)

	out, err := resolver(context.Background(), "01957c8c-2222-7000-bbbb-2222bbbb2222")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}

	// History Familiar (Curie, encouraging, teen, apprentice tier → 1 tool only)
	if !strings.Contains(out.Instruction, "Curie") {
		t.Errorf("want Curie in instruction; got %q", out.Instruction)
	}
	if !strings.Contains(out.Instruction, "encouraging") {
		t.Errorf("want encouraging tone; got %q", out.Instruction)
	}
	if len(out.AllowedTools) != 1 {
		t.Errorf("apprentice tier has 1 skill slot; got %d allowed tools", len(out.AllowedTools))
	}
}

func TestFamiliarResolver_distinguishesFamiliarsBySpecialization(t *testing.T) {
	// IMDA D2 transparency: same input → same output, different inputs → different outputs.
	reg := skillregistry.NewStubRegistry()
	avail := newStubAvailable("atom.cite")
	resolver, _ := NewFamiliarResolver(reg, avail)

	mathOut, _ := resolver(context.Background(), "01957c8c-1111-7000-aaaa-1111aaaa1111")
	historyOut, _ := resolver(context.Background(), "01957c8c-2222-7000-bbbb-2222bbbb2222")
	codingOut, _ := resolver(context.Background(), "01957c8c-3333-7000-cccc-3333cccc3333")

	if mathOut.Instruction == historyOut.Instruction {
		t.Error("math + history should resolve to distinct instructions")
	}
	if historyOut.Instruction == codingOut.Instruction {
		t.Error("history + coding should resolve to distinct instructions")
	}
	if mathOut.Instruction == codingOut.Instruction {
		t.Error("math + coding should resolve to distinct instructions")
	}
}

func TestFamiliarResolver_propagatesRegistryNotFound(t *testing.T) {
	reg := skillregistry.NewStubRegistry()
	avail := newStubAvailable("atom.search")
	resolver, _ := NewFamiliarResolver(reg, avail)

	_, err := resolver(context.Background(), "01999999-9999-7000-9999-999999999999")
	if !errors.Is(err, skillregistry.ErrFamiliarNotFound) {
		t.Errorf("want wrapped ErrFamiliarNotFound; got %v", err)
	}
}

func TestFamiliarResolver_errorsOnConfigInvariantViolation(t *testing.T) {
	// Synthetic registry returns a config with AllowedSkills > SkillSlotsUnlocked
	// — the resolver should refuse rather than serve a malformed config.
	bad := &skillregistry.FamiliarConfig{
		FamiliarID:         "bad-cfg",
		Name:               "Broken",
		Specialization:     "x",
		GrowthStage:        1, // hatched — must not trigger pre-hatch guard before cap check
		AllowedSkills:      []string{"atom.search", "persona.voice"},
		SkillSlotsUnlocked: 1, // cap violation
	}
	reg := &staticRegistry{cfg: bad}
	avail := newStubAvailable("atom.search", "persona.voice")
	resolver, _ := NewFamiliarResolver(reg, avail)

	_, err := resolver(context.Background(), "bad-cfg")
	if err == nil {
		t.Fatal("want error for cap violation; got nil")
	}
}

func TestFamiliarResolver_skipsUnknownAllowedTool(t *testing.T) {
	// R4-2 (CHO-2013 P1.B): an allowed_tools entry this build does not ship
	// SKIPS with a loud WARN — never kills the familiar's session.
	bad := &skillregistry.FamiliarConfig{
		FamiliarID:         "bad-cfg",
		AllowedTools:       []string{"nonexistent.tool", "atom.search"},
		SkillSlotsUnlocked: 1,
		GrowthStage:        2, // hatched — the pre-hatch guard is a separate concern
	}
	reg := &staticRegistry{cfg: bad}
	avail := newStubAvailable("atom.search")
	resolver, _ := NewFamiliarResolver(reg, avail)

	out, err := resolver(context.Background(), "bad-cfg")
	if err != nil {
		t.Fatalf("unknown allowed tool must skip, not error: %v", err)
	}
	if len(out.AllowedTools) != 1 || out.AllowedTools[0] != "atom.search" {
		t.Fatalf("want [atom.search]; got %v", out.AllowedTools)
	}
}

// ---------- ValidateConfigInvariants ----------

func TestValidateConfigInvariants_rejectsNilCfg(t *testing.T) {
	if err := ValidateConfigInvariants(nil); err == nil {
		t.Fatal("want error for nil cfg; got nil")
	}
}

func TestValidateConfigInvariants_rejectsExceededSlotCap(t *testing.T) {
	cfg := &skillregistry.FamiliarConfig{
		AllowedSkills:      []string{"a", "b", "c"},
		SkillSlotsUnlocked: 2,
	}
	if err := ValidateConfigInvariants(cfg); err == nil {
		t.Fatal("want error for cap violation; got nil")
	}
}

func TestValidateConfigInvariants_rejectsDuplicateSkill(t *testing.T) {
	cfg := &skillregistry.FamiliarConfig{
		AllowedSkills:      []string{"a", "a"},
		SkillSlotsUnlocked: 2,
	}
	if err := ValidateConfigInvariants(cfg); err == nil {
		t.Fatal("want error for duplicate; got nil")
	}
}

func TestValidateConfigInvariants_passesValidConfig(t *testing.T) {
	cfg := &skillregistry.FamiliarConfig{
		AllowedSkills:      []string{"a", "b"},
		SkillSlotsUnlocked: 3,
	}
	if err := ValidateConfigInvariants(cfg); err != nil {
		t.Errorf("unexpected: %v", err)
	}
}

func TestValidateConfigInvariants_passesEmptyAllowedSkills(t *testing.T) {
	cfg := &skillregistry.FamiliarConfig{
		AllowedSkills:      nil,
		SkillSlotsUnlocked: 1,
	}
	if err := ValidateConfigInvariants(cfg); err != nil {
		t.Errorf("unexpected: %v", err)
	}
}

// ---------- helpers ----------

// staticRegistry returns the same cfg regardless of input. Lets tests
// inject malformed configs to verify the resolver's invariant checks.
type staticRegistry struct{ cfg *skillregistry.FamiliarConfig }

func (s *staticRegistry) LoadFamiliarConfig(ctx context.Context, id string) (*skillregistry.FamiliarConfig, error) {
	return s.cfg, nil
}

// Compile-time assertion: NewFamiliarResolver returns instancedispatch.Resolver
// — the resolver must be drop-in usable by instancedispatch.New(...).
var _ instancedispatch.Resolver = func(ctx context.Context, id string) (instancedispatch.InstanceConfig, error) {
	return instancedispatch.InstanceConfig{}, nil
}

// Compile-time assertion: stubTool satisfies tool.Tool (from builder_test.go).
var _ tool.Tool = stubTool{}

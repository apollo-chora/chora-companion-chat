// Package skillregistry resolves a Familiar instance's per-session configuration
// at agent bootstrap time per ADR-147 §7 (session-time per-instance config pattern)
// and `docs/architecture/multi-familiar-per-user-2026-05-11.md` §2.1.
//
// Production: backed by the chora_consumption.familiars + familiar_skill_grants +
// familiar_progression_tiers tables (per chora-consumption/migrations/ M14.1
// schema). POC: in-memory stub with three canned Familiars (math / history /
// coding) keyed by familiar_id.
//
// One Familiar config = one row in chora_consumption.familiars + its grants.
// The agent code is identical across all Familiars; per-Familiar variance lives
// in this configuration data (tool list, instruction rules, memory scope,
// RAG specialization).
package skillregistry

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrFamiliarNotFound is returned when a familiar_id has no config.
var ErrFamiliarNotFound = errors.New("familiar not found")

// FamiliarConfig is the per-Familiar session-bootstrap payload.
//
// All variance axes per ADR-147 §7 + multi-familiar-per-user-2026-05-11.md §2:
//   - tool list (AllowedSkills → resolved against availableTools map)
//   - instruction prompt (woven by agent.ComposeInstruction from these fields)
//   - memory scope (app_name = "familiar:{FamiliarID}" — caller's
//     responsibility, propagated via session.AppName by the launcher)
//   - RAG slice (Specialization passed to chora-creation.SearchEmbeddings
//     gRPC scope=FAMILIAR_SPECIALIZED filter)
//   - billing label (FamiliarID injected by chora-adk-common/manaplugin)
type FamiliarConfig struct {
	FamiliarID            string
	OwnerGCID             string
	TenantID              string
	Name                  string // user-chosen ("Newton", "Curie", ...)
	Specialization        string // "math" | "history" | "coding" | "music" | ...
	AgeStage              string // toddler | child | teen | young_adult | adult (per ADR-116)
	YoungForever          bool
	EvolutionTier         string // apprentice | adept | master | sage (user-XP-gated)
	SkillSlotsUnlocked    int
	MemoryContextCapacity int
	PersonaSummary        string
	// GuidanceNote is the learner's fenced free-text Persona note (CHO-2015,
	// ADR-219 D2; ≤280 chars, Model-Armor-screened at save). UNTRUSTED —
	// ComposeInstruction injects it ONLY inside a locked-frame data fence,
	// never in an instruction position.
	GuidanceNote    string
	ConfiguredRules ConfiguredRules
	AllowedSkills   []string // equipped skill_key list, ≤ SkillSlotsUnlocked entries
	// AllowedTools is the MERGED runtime tool allowlist (CHO-2013 P1.B,
	// R4-2): innate(stage) canonical names ∪ equipped Skills' bindings,
	// computed server-side by consumption. The per-turn filter keys on
	// THIS list; AllowedSkills stays the skill KEYS (cap + prompt weave).
	AllowedTools    []string
	AppliedAccentID string // optional voice accent (per ADR-116)

	// LearnerPersona is the orthogonal learner-axis overlay added by the
	// ADR-116 learner-persona amendment (Iter 1, M14 BLANKET). Distinct from
	// Specialization (which is the Familiar's subject domain).
	//
	// Values: "curious-explorer" | "cert-focused" | "social-leader". Drives:
	//   - few-shot example selection in agent.ComposeInstruction
	//   - CREATE [AUDIENCE] block tone calibration
	//
	// Empty string = default to "curious-explorer" at instruction-compose time
	// (lets pre-amendment Familiars continue functioning without migration).
	LearnerPersona string

	// --- ADR-149 Growth-Stage axis (Iter G.4) ------------------------------
	//
	// GrowthStage is the per-Familiar capability axis (0-6) per ADR-149
	// §"The 7 stages": 0=Egg → 1=Baby → 2=Fledgling → 3=Awakened →
	// 4=Structural → 5=Teen → 6=Matured. Supersedes ADR-116 §age_stage in
	// capability gating; AgeStage above is retained for voice-pitch +
	// persona-character only.
	GrowthStage int

	// Species is the per-Familiar breed assigned at hatching per ADR-149
	// §"Breed lootbox" — one of {owl|fox|cat|dragon|phoenix|turtle|wolf|raven}.
	// Empty string = unhatched / legacy backfill row.
	Species string

	// ShinyVariant is the cosmetic alternate-palette marker rolled when a
	// learner pulls a breed already in their roster (ADR-149 §"Breed
	// lootbox"). Purely visual — no capability difference.
	ShinyVariant bool

	// ResonantAtomID anchors the Familiar's conversational competence to a
	// specific LearningAtom (UUIDv7) chosen at hatching per ADR-149
	// §"Resonant Atom anchor". The Familiar's KG reach is bounded by graph
	// distance from this anchor. Immutable post-hatching.
	ResonantAtomID string

	// VisibleKgNeighborAtomIDs is the per-Familiar list of KG neighbour atom
	// IDs revealed via the ADR-143 hexagon mechanic. Stage 2 picks one;
	// Stages 3-5 progressively add auto-discovered neighbours. Propagated
	// to chora-adk-common/growthstageplugin via session.State
	// `visible_kg_neighbors`.
	VisibleKgNeighborAtomIDs []string

	// AhaMomentActiveUntil marks the END of the 24-hour Stage-3 Aha-moment
	// preview window per ADR-149 §"Source revelation". Zero value = no
	// active window. Propagated to chora-adk-common/ahamomentplugin via
	// session.State `aha_moment_active_until`.
	AhaMomentActiveUntil time.Time
}

// ConfiguredRules is the JSONB blob `chora_consumption.familiars.configured_rules`
// expanded into a typed struct for prompt composition.
type ConfiguredRules struct {
	Tone               string     // "socratic" | "direct" | "encouraging"
	HintPolicy         HintPolicy //
	DifficultyCap      string     // "foundation" | "intermediate" | "advanced"
	Language           string     // ISO 639-1 (e.g., "en")
	CitationStrictness string     // "strict" | "lenient"
	// CHO-2015 (ADR-219 D2) — learner Persona knobs.
	AddressStyle  string   // "first_name" | "nickname" | "formal"
	InterestChips []string // bounded topic tags to weave in when natural
}

// HintPolicy controls how many hints are surfaced before the answer is revealed.
type HintPolicy struct {
	MaxHintsBeforeReveal int
	Progression          string // "ladder" | "uniform"
}

// Registry resolves Familiar configs by familiar_id.
type Registry interface {
	LoadFamiliarConfig(ctx context.Context, familiarID string) (*FamiliarConfig, error)
}

// StubRegistry is the POC in-memory implementation. Pre-seeded with three
// canned Familiars (math / history / coding) for sandbox testing. Production
// implementation replaces this with a gRPC client to chora-consumption.
type StubRegistry struct {
	configs map[string]*FamiliarConfig
}

// NewStubRegistry seeds three canned Familiars under deterministic UUIDs.
func NewStubRegistry() *StubRegistry {
	r := &StubRegistry{
		configs: make(map[string]*FamiliarConfig, 3),
	}
	for _, c := range stubFamiliars() {
		r.configs[c.FamiliarID] = c
	}
	return r
}

// LoadFamiliarConfig satisfies Registry. Returns ErrFamiliarNotFound if the
// familiar_id is unknown to the registry.
func (r *StubRegistry) LoadFamiliarConfig(ctx context.Context, familiarID string) (*FamiliarConfig, error) {
	if strings.TrimSpace(familiarID) == "" {
		return nil, fmt.Errorf("%w: familiar_id is empty", ErrFamiliarNotFound)
	}
	cfg, ok := r.configs[familiarID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrFamiliarNotFound, familiarID)
	}
	// Defensive copy — callers must not mutate the registry's stored config.
	copy := *cfg
	copy.AllowedSkills = append([]string(nil), cfg.AllowedSkills...)
	copy.AllowedTools = append([]string(nil), cfg.AllowedTools...)
	copy.VisibleKgNeighborAtomIDs = append([]string(nil), cfg.VisibleKgNeighborAtomIDs...)
	return &copy, nil
}

// Canonical canned Familiars. Deterministic UUIDs so tests + sandbox runs
// reproduce. UUIDs use the v7 "01957c8c-..." prefix from the existing tool tests.

const (
	stubMathFamiliarID    = "01957c8c-1111-7000-aaaa-1111aaaa1111"
	stubHistoryFamiliarID = "01957c8c-2222-7000-bbbb-2222bbbb2222"
	stubCodingFamiliarID  = "01957c8c-3333-7000-cccc-3333cccc3333"
	stubOwnerGCID         = "01957c8c-0000-7000-9999-000099990000"
	stubTenantID          = "01957c8c-0000-7000-8888-000088880000"

	// Stub Resonant Atom anchors (one per stub Familiar) — deterministic UUIDs.
	stubMathResonantAtomID    = "01957c8c-4444-7000-aaaa-4444aaaa4444"
	stubHistoryResonantAtomID = "01957c8c-5555-7000-bbbb-5555bbbb5555"
	stubCodingResonantAtomID  = "01957c8c-6666-7000-cccc-6666cccc6666"
)

func stubFamiliars() []*FamiliarConfig {
	return []*FamiliarConfig{
		{
			FamiliarID:            stubMathFamiliarID,
			OwnerGCID:             stubOwnerGCID,
			TenantID:              stubTenantID,
			Name:                  "Newton",
			Specialization:        "math",
			AgeStage:              "child",
			YoungForever:          false,
			EvolutionTier:         "adept",
			SkillSlotsUnlocked:    3,
			MemoryContextCapacity: 5000,
			PersonaSummary: "Patient guide to algebra and arithmetic. " +
				"Uses everyday analogies (cooking, sports) to make abstract concepts concrete.",
			ConfiguredRules: ConfiguredRules{
				Tone:               "socratic",
				HintPolicy:         HintPolicy{MaxHintsBeforeReveal: 3, Progression: "ladder"},
				DifficultyCap:      "intermediate",
				Language:           "en",
				CitationStrictness: "strict",
			},
			// ADR-249 A1a: the agent ships exactly one tool (atom.cite); the
			// stub allowlists mirror that so sandbox turns exercise reality.
			AllowedSkills:  []string{"atom.cite"},
			AllowedTools:   []string{"atom.cite"},
			LearnerPersona: "curious-explorer",

			// ADR-149: Newton is a Stage 2 Fledgling Owl (the "wise mentor" archetype
			// for math). Ladder grants cite_atom (shipped); one user-pick KG neighbour.
			GrowthStage:    2,
			Species:        "owl",
			ShinyVariant:   false,
			ResonantAtomID: stubMathResonantAtomID,
			VisibleKgNeighborAtomIDs: []string{
				stubMathResonantAtomID + "-neighbor-0", // user-pick at Stage 2
			},
			// No active Aha-moment window for Newton (he's not yet at Stage 3).
		},
		{
			FamiliarID:            stubHistoryFamiliarID,
			OwnerGCID:             stubOwnerGCID,
			TenantID:              stubTenantID,
			Name:                  "Curie",
			Specialization:        "history",
			AgeStage:              "teen",
			YoungForever:          true,
			EvolutionTier:         "apprentice",
			SkillSlotsUnlocked:    1,
			MemoryContextCapacity: 1000,
			PersonaSummary: "Eager storyteller of the past. " +
				"Frames eras in terms of the people who lived them.",
			ConfiguredRules: ConfiguredRules{
				Tone:               "encouraging",
				HintPolicy:         HintPolicy{MaxHintsBeforeReveal: 2, Progression: "ladder"},
				DifficultyCap:      "foundation",
				Language:           "en",
				CitationStrictness: "strict",
			},
			AllowedSkills:  []string{"atom.cite"},
			AllowedTools:   []string{"atom.cite"},
			LearnerPersona: "cert-focused",

			// ADR-149: Curie is a Stage 4 Structural Fox (the "clever scout"
			// archetype for history). Persistent episodic RAG + persona_lookup.
			GrowthStage:    4,
			Species:        "fox",
			ShinyVariant:   false,
			ResonantAtomID: stubHistoryResonantAtomID,
			VisibleKgNeighborAtomIDs: []string{
				stubHistoryResonantAtomID + "-neighbor-0",
				stubHistoryResonantAtomID + "-neighbor-1",
				stubHistoryResonantAtomID + "-neighbor-2",
				stubHistoryResonantAtomID + "-neighbor-3",
			},
		},
		{
			FamiliarID:            stubCodingFamiliarID,
			OwnerGCID:             stubOwnerGCID,
			TenantID:              stubTenantID,
			Name:                  "Lovelace",
			Specialization:        "coding",
			AgeStage:              "young_adult",
			YoungForever:          false,
			EvolutionTier:         "master",
			SkillSlotsUnlocked:    5,
			MemoryContextCapacity: 15000,
			PersonaSummary: "Pragmatic mentor for first programs and debugging. " +
				"Asks for hypotheses before suggesting fixes.",
			ConfiguredRules: ConfiguredRules{
				Tone:               "direct",
				HintPolicy:         HintPolicy{MaxHintsBeforeReveal: 1, Progression: "uniform"},
				DifficultyCap:      "advanced",
				Language:           "en",
				CitationStrictness: "strict",
			},
			AllowedSkills:  []string{"atom.cite"},
			AllowedTools:   []string{"atom.cite"},
			LearnerPersona: "social-leader",

			// ADR-149: Lovelace is a Stage 6 Matured Dragon (the "old-soul mentor"
			// archetype for coding). Full toolkit, full procedural memory, full KG.
			GrowthStage:    6,
			Species:        "dragon",
			ShinyVariant:   true, // duplicate-roll cosmetic marker
			ResonantAtomID: stubCodingResonantAtomID,
			VisibleKgNeighborAtomIDs: []string{
				stubCodingResonantAtomID + "-neighbor-0",
				stubCodingResonantAtomID + "-neighbor-1",
				stubCodingResonantAtomID + "-neighbor-2",
				stubCodingResonantAtomID + "-neighbor-3",
				stubCodingResonantAtomID + "-neighbor-4",
				stubCodingResonantAtomID + "-neighbor-5",
			},
		},
	}
}

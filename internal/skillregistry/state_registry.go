// state_registry.go — the production Registry: per-Familiar config read from
// the ADK session state.
//
// History (ADR-249 §1.3): a full consumption gRPC client (GRPCRegistry) was
// built to fix the 2026-06-02 zero-output defect, then reversed, because the
// sidecar-less agent (ADR-169) cannot dial consumption's STRICT-mTLS :9090.
// That dead client was deleted under ADR-249 A1a; this state-backed registry
// is the recorded design: consumption resolves the config server-side
// (ResolveCompanionConfig) and injects it at CreateSession.
package skillregistry

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/apollo-chora/chora-adk-common/instancedispatch"
	consumptionv1 "github.com/apollo-chora/chora-contracts/gen/go/chora/services/consumption/v1"
)

// StateRegistry resolves FamiliarConfig from the per-instance config payload
// consumption injects into the ADK CreateSession state. instancedispatch lifts
// session.State()["familiar_config"] (a protojson CompanionInstanceConfig) into
// the resolver ctx; this registry reads + deserializes it. NO gRPC call to
// consumption — the sidecar-less agent can't reach consumption's STRICT-mTLS
// :9090.
type StateRegistry struct{}

// NewStateRegistry constructs the state-backed registry.
func NewStateRegistry() *StateRegistry { return &StateRegistry{} }

// LoadFamiliarConfig satisfies Registry. Reads the protojson CompanionInstanceConfig
// stamped onto ctx (from session state) and maps it to FamiliarConfig.
func (r *StateRegistry) LoadFamiliarConfig(ctx context.Context, familiarID string) (*FamiliarConfig, error) {
	if strings.TrimSpace(familiarID) == "" {
		return nil, fmt.Errorf("%w: familiar_id is empty", ErrFamiliarNotFound)
	}
	cfgJSON := instancedispatch.FamiliarConfigJSONFromContext(ctx)
	if strings.TrimSpace(cfgJSON) == "" {
		// Fail-loud: consumption MUST inject familiar_config into the
		// CreateSession state. Absence is a wiring bug, not "not found".
		return nil, fmt.Errorf(
			"skillregistry: no familiar_config in session state for familiar %s "+
				"(consumption must inject it at CreateSession)", familiarID)
	}
	var pc consumptionv1.CompanionInstanceConfig
	if err := protojson.Unmarshal([]byte(cfgJSON), &pc); err != nil {
		return nil, fmt.Errorf("skillregistry: unmarshal familiar_config for %s: %w", familiarID, err)
	}
	return protoToFamiliarConfig(&pc), nil
}

// Compile-time guarantee that *StateRegistry satisfies Registry.
var _ Registry = (*StateRegistry)(nil)

// protoToFamiliarConfig maps the gRPC CompanionInstanceConfig onto the agent's
// FamiliarConfig (field-for-field; see familiar_growth_service.proto comment).
func protoToFamiliarConfig(p *consumptionv1.CompanionInstanceConfig) *FamiliarConfig {
	if p == nil {
		return &FamiliarConfig{}
	}
	cfg := &FamiliarConfig{
		FamiliarID:               p.GetCompanionId(),
		OwnerGCID:                p.GetOwnerGcid(),
		TenantID:                 p.GetTenantId(),
		Name:                     p.GetName(),
		Specialization:           p.GetSpecialization(),
		AgeStage:                 p.GetAgeStage(),
		YoungForever:             p.GetYoungForever(),
		EvolutionTier:            p.GetEvolutionTier(),
		SkillSlotsUnlocked:       int(p.GetSkillSlotsUnlocked()),
		MemoryContextCapacity:    int(p.GetMemoryContextCapacity()),
		PersonaSummary:           p.GetPersonaSummary(),
		GuidanceNote:             p.GetGuidanceNote(),
		AllowedSkills:            append([]string(nil), p.GetAllowedSkills()...),
		AllowedTools:             append([]string(nil), p.GetAllowedTools()...),
		AppliedAccentID:          p.GetAppliedAccentId(),
		LearnerPersona:           p.GetLearnerPersona(),
		GrowthStage:              int(p.GetGrowthStage()),
		Species:                  p.GetSpecies(),
		ShinyVariant:             p.GetShinyVariant(),
		ResonantAtomID:           p.GetResonantAtomId(),
		VisibleKgNeighborAtomIDs: append([]string(nil), p.GetVisibleKgNeighborAtomIds()...),
	}
	if rules := p.GetConfiguredRules(); rules != nil {
		cfg.ConfiguredRules = ConfiguredRules{
			Tone: rules.GetTone(),
			HintPolicy: HintPolicy{
				MaxHintsBeforeReveal: int(rules.GetMaxHintsBeforeReveal()),
				Progression:          rules.GetHintProgression(),
			},
			DifficultyCap:      rules.GetDifficultyCap(),
			Language:           rules.GetLanguage(),
			CitationStrictness: rules.GetCitationStrictness(),
			AddressStyle:       rules.GetAddressStyle(),
			InterestChips:      append([]string(nil), rules.GetInterestChips()...),
		}
	}
	if ts := p.GetAhaMomentActiveUntil(); ts != nil {
		cfg.AhaMomentActiveUntil = ts.AsTime()
	}
	return cfg
}

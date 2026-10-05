package agent

// Pre-hatch refusal for the Companion chat lane.
//
// This file previously also carried the managed memory-bank write path
// (ErrMemoryWriteDeferred, AddFamiliarSessionToMemory, ShouldWriteMemory,
// MemoryBankAppNameForFamiliar). The managed runtime was decommissioned by
// ADR-169 and per-Companion memory is now pgvector RAG in chora_consumption
// per ADR-173, so that path was removed: it was gated on a runtime id env
// var, which this agent sets in no manifest and no live pod, making the
// memory service permanently nil and every write a no-op.
//
// What remains is the GrowthStage-0 refusal, which never had anything to do
// with the memory bank.

import (
	"errors"
	"fmt"

	"github.com/apollo-chora/chora-companion-chat/internal/skillregistry"
)

// ErrFamiliarPreHatch is returned by RefusePreHatch and propagated by
// NewFamiliarResolver when the Familiar's GrowthStage is 0 (Mystery Egg,
// unbound). The resolver error is observed by instancedispatch.BeforeRunCallback,
// which refuses the turn: the reason string "familiar_pre_hatch" is included
// in the error so terminationplugin observers can classify it.
var ErrFamiliarPreHatch = errors.New("familiar_pre_hatch")

// RefusePreHatch returns ErrFamiliarPreHatch when cfg represents a pre-hatch
// Familiar (GrowthStage == 0). Returns nil for nil cfg (nil-check is upstream
// caller's responsibility via ValidateConfigInvariants).
//
// Called from the NewFamiliarResolver closure in dispatch.go to enforce the
// pre-hatch refusal at the resolver level.
func RefusePreHatch(cfg *skillregistry.FamiliarConfig) error {
	if cfg == nil {
		return nil // nil-check deferred to ValidateConfigInvariants
	}
	if cfg.GrowthStage == 0 {
		return fmt.Errorf("familiar %s: %w (growth_stage=0; pre-hatch Familiars cannot participate in chat sessions)",
			cfg.FamiliarID, ErrFamiliarPreHatch)
	}
	return nil
}

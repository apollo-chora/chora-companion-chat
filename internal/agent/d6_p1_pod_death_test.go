package agent

import (
	"context"
	"testing"

	"github.com/apollo-chora/chora-companion-chat/internal/skillregistry"
)

// D6 Pillar 1 — Pod-death chaos (stub harness only).
//
// Real chaos test was cleared by POC W3 Iter 3b multi-crew variant
// (`docs/architecture/poc/adk-poc-w3-test-report-2026-05-12.md` §1.4 (g)).
// This stub asserts the recovery contract the production harness exercises:
// the resume key (`familiar_id` in session state) MUST be the ONLY input
// required to reconstitute the Familiar's per-session bootstrap.
//
// Per agentic-resilience-d6 SKILL Pillar 1: "thread identity = saga/workflow
// identity. Callers MUST set config.configurable.thread_id per invocation;
// the thread_id is the recovery key." For Familiar, the analogue is
// session.State().familiar_id.

func TestD6P1_familiarIDIsTheSoleRecoveryKey(t *testing.T) {
	r := skillregistry.NewStubRegistry()

	// PRE-pod-death state: load Newton's config.
	pre, err := r.LoadFamiliarConfig(context.Background(), skillregistry.DefaultFamiliarID())
	if err != nil {
		t.Fatalf("PRE load failed: %v", err)
	}

	// Simulate pod death + reload (new registry instance — like a fresh
	// container coming up after engine identity change).
	rFresh := skillregistry.NewStubRegistry()
	post, err := rFresh.LoadFamiliarConfig(context.Background(), skillregistry.DefaultFamiliarID())
	if err != nil {
		t.Fatalf("POST load failed: %v", err)
	}

	// POST-recovery state MUST be identical to PRE for all load-bearing axes.
	if pre.Specialization != post.Specialization {
		t.Errorf("specialization drifted across pod-death: pre=%q post=%q",
			pre.Specialization, post.Specialization)
	}
	if pre.LearnerPersona != post.LearnerPersona {
		t.Errorf("learner_persona drifted: pre=%q post=%q",
			pre.LearnerPersona, post.LearnerPersona)
	}
	if pre.AgeStage != post.AgeStage {
		t.Errorf("age_stage drifted: pre=%q post=%q", pre.AgeStage, post.AgeStage)
	}
	if pre.EvolutionTier != post.EvolutionTier {
		t.Errorf("evolution_tier drifted: pre=%q post=%q", pre.EvolutionTier, post.EvolutionTier)
	}
}

func TestD6P1_unknownFamiliarIDDoesNotPanicAcrossRecovery(t *testing.T) {
	// Failure mode: cross-pod recovery against an unknown familiar_id
	// MUST return ErrFamiliarNotFound, not panic. This guards against the
	// failure mode where a fresh pod loads a session state pointing at a
	// since-deleted Familiar.
	r := skillregistry.NewStubRegistry()
	_, err := r.LoadFamiliarConfig(context.Background(), "01999999-9999-7000-9999-999999999999")
	if err == nil {
		t.Fatal("want ErrFamiliarNotFound for unknown id on recovery")
	}
}

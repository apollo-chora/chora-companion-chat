package agent

import (
	"testing"
)

// D6 Pillar 4 — trace emission (stub harness only).
//
// Real test was cleared by POC W3 Iter 4 OTLP stdout verification — every
// trace span MUST carry the 5 mandatory Chora attributes (per
// agentic-resilience-d6 SKILL Pillar 4) + GenAI semantic conventions on
// LLM calls.
//
// Familiar's mandatory span attribute set (extends the SKILL's base list
// with the per-Familiar discriminators):
//
//	chora.tenant_id       — load-bearing for Pillar 3 isolation audits
//	chora.familiar_id     — per-instance discriminator (per ADR-147 §7)
//	chora.mana_tier       — per-tier cost attribution (Iter 7 lever)
//	chora.crew_kind       — multi-crew safety
//	gen_ai.request.model  — per-tier model selection evidence
//	gen_ai.usage.output_tokens — per-tier MaxOutputTokens enforcement evidence
//
// Production tests will exercise this against the OTLP stdout exporter
// after deploy. The stub asserts the attribute name list stays in sync
// with the resilience contract.

func TestD6P4_mandatoryAttributeNamesCoverChoraInstancing(t *testing.T) {
	required := []string{
		"chora.tenant_id",
		"chora.familiar_id",
		"chora.mana_tier",
		"chora.crew_kind",
		"gen_ai.request.model",
		"gen_ai.usage.output_tokens",
	}

	// MandatorySpanAttributes() is the canonical list exported by the
	// agent package so production wiring (manaplugin / tieredmodelplugin /
	// instancedispatch) can cross-reference it.
	got := MandatorySpanAttributes()
	gotSet := make(map[string]struct{}, len(got))
	for _, k := range got {
		gotSet[k] = struct{}{}
	}
	for _, k := range required {
		if _, ok := gotSet[k]; !ok {
			t.Errorf("MandatorySpanAttributes() missing %q (D6 P4 contract drift)", k)
		}
	}
}

func TestD6P4_mandatoryAttributeListHasNoDuplicates(t *testing.T) {
	got := MandatorySpanAttributes()
	seen := make(map[string]struct{}, len(got))
	for _, k := range got {
		if _, dup := seen[k]; dup {
			t.Errorf("duplicate attribute %q in MandatorySpanAttributes()", k)
		}
		seen[k] = struct{}{}
	}
}

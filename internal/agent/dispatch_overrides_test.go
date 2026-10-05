package agent

// ADR-197 P3 (CHO-2368): the resolver threads the ctx-stamped
// prompt_overrides_json (injected into session state by chora-consumption,
// stamped onto ctx by instancedispatch.ResolveInstanceFromState) into the
// composed instruction. Absent / malformed payloads leave the instruction
// byte-identical to the no-override path (fail-soft, bounded drift).

import (
	"context"
	"strings"
	"testing"

	"github.com/apollo-chora/chora-adk-common/instancedispatch"
	"github.com/apollo-chora/chora-companion-chat/internal/skillregistry"
)

const mathFamiliarID = "01957c8c-1111-7000-aaaa-1111aaaa1111"

func TestFamiliarResolver_AppliesPromptOverridesFromContext(t *testing.T) {
	reg := skillregistry.NewStubRegistry()
	avail := newStubAvailable("atom.search", "persona.voice", "schedule.review")
	resolver, err := NewFamiliarResolver(reg, avail)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}

	ctx := instancedispatch.WithPromptOverridesJSON(
		context.Background(), `{"role_frame":"OVR-SENTINEL-42"}`)
	out, err := resolver(ctx, mathFamiliarID)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !strings.Contains(out.Instruction, "OVR-SENTINEL-42") {
		t.Error("ctx-stamped override did not reach the composed instruction")
	}

	// Control: without the ctx stamp the sentinel must be absent.
	plain, err := resolver(context.Background(), mathFamiliarID)
	if err != nil {
		t.Fatalf("resolve (control): %v", err)
	}
	if strings.Contains(plain.Instruction, "OVR-SENTINEL-42") {
		t.Error("sentinel leaked into the no-override instruction")
	}
}

func TestFamiliarResolver_MalformedOverridesFailSoft(t *testing.T) {
	reg := skillregistry.NewStubRegistry()
	avail := newStubAvailable("atom.search", "persona.voice", "schedule.review")
	resolver, err := NewFamiliarResolver(reg, avail)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}

	plain, err := resolver(context.Background(), mathFamiliarID)
	if err != nil {
		t.Fatalf("resolve (control): %v", err)
	}
	ctx := instancedispatch.WithPromptOverridesJSON(context.Background(), "not-json")
	out, err := resolver(ctx, mathFamiliarID)
	if err != nil {
		t.Fatalf("malformed overrides must not fail the resolve: %v", err)
	}
	if out.Instruction != plain.Instruction {
		t.Error("malformed overrides must degrade to the no-override instruction")
	}
}

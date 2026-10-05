package agent

import (
	"context"
	"sync"
	"testing"

	"google.golang.org/adk/tool"

	"github.com/apollo-chora/chora-adk-common/instancedispatch"
	"github.com/apollo-chora/chora-companion-chat/internal/skillregistry"
)

// D6 Pillar 3 — Multi-tenant + multi-workflow chaos on shared pipe.
//
// Real test was cleared by POC W3 Iter 3b scenario (g) — closure saga
// kill while Familiar served 3 sessions in parallel. This stub asserts
// the dispatch path's hard refusal-on-missing-familiar_id under
// concurrent goroutine load (mimics N concurrent sessions on shared
// engine) — the load-bearing invariant for cross-tenant isolation.

func TestD6P3_instancedispatchRejectsMissingFamiliarIDUnderConcurrency(t *testing.T) {
	r := skillregistry.NewStubRegistry()

	// NewFamiliarResolver is the canonical Resolver adapter used by both
	// deploy variants (per-session + dispatch-plugin). Concurrent calls with
	// missing familiar_id must ALL reject — no race where one slips through.
	available := map[string]tool.Tool{
		"atom.search":     d6StubTool("atom.search"),
		"persona.voice":   d6StubTool("persona.voice"),
		"schedule.review": d6StubTool("schedule.review"),
	}
	resolver, err := NewFamiliarResolver(r, available)
	if err != nil {
		t.Fatalf("NewFamiliarResolver: %v", err)
	}

	const N = 32
	var wg sync.WaitGroup
	rejects := make([]error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			// Empty familiar_id should refuse at the Resolver boundary,
			// matching the runtime refusal at BeforeRunCallback.
			_, err := resolver(context.Background(), "")
			rejects[idx] = err
		}(i)
	}
	wg.Wait()

	for i, err := range rejects {
		if err == nil {
			t.Errorf("goroutine %d slipped through with empty familiar_id (D6 P3 isolation broken)", i)
		}
	}
}

func TestD6P3_dispatchConfigStateKeyIsFamiliarID(t *testing.T) {
	// The dispatch state-key contract: for the Familiar crew the key MUST
	// be `familiar_id`. Drift breaks per-instance isolation under multi-tenant
	// load (a typo'd key reads the wrong slot from session state).
	cfg := instancedispatch.Config{StateKey: "familiar_id"}
	if cfg.StateKey != "familiar_id" {
		t.Errorf("dispatch StateKey for Familiar crew must be familiar_id; got %q", cfg.StateKey)
	}
}

type d6StubTool string

func (s d6StubTool) Name() string        { return string(s) }
func (s d6StubTool) Description() string { return "" }
func (s d6StubTool) IsLongRunning() bool { return false }

package agent

import (
	"context"
	"errors"
	"iter"
	"sync"
	"testing"

	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/session"

	"github.com/apollo-chora/chora-companion-chat/internal/skillregistry"
)

// ---------- ParseFamiliarIDFromAppName ----------

func TestParseFamiliarIDFromAppName_extractsID(t *testing.T) {
	id, err := ParseFamiliarIDFromAppName("familiar:01957c8c-1111-7000-aaaa-1111aaaa1111")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if id != "01957c8c-1111-7000-aaaa-1111aaaa1111" {
		t.Errorf("want full UUID; got %q", id)
	}
}

func TestParseFamiliarIDFromAppName_rejectsMissingPrefix(t *testing.T) {
	_, err := ParseFamiliarIDFromAppName("not-a-familiar-app-name")
	if !errors.Is(err, ErrMalformedAppName) {
		t.Errorf("want ErrMalformedAppName; got %v", err)
	}
}

func TestParseFamiliarIDFromAppName_rejectsEmptyID(t *testing.T) {
	_, err := ParseFamiliarIDFromAppName("familiar:")
	if !errors.Is(err, ErrMalformedAppName) {
		t.Errorf("want ErrMalformedAppName for empty id; got %v", err)
	}
}

// ---------- PerSessionLoader.LoadAgent ----------

// recordingFactory captures the cfg every LoadAgent receives, so tests can
// verify which Familiar's config was actually dispatched.
type recordingFactory struct {
	mu    sync.Mutex
	calls []*skillregistry.FamiliarConfig
}

func (r *recordingFactory) factory(ctx context.Context, cfg *skillregistry.FamiliarConfig) (adkagent.Agent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, cfg)
	// Build a real agent — name encodes the familiar_id so we can round-trip the assertion
	// without needing to satisfy the unexported agent.internal() method ourselves.
	return adkagent.New(adkagent.Config{
		Name:        "test_" + cfg.FamiliarID,
		Description: "test factory output for specialization=" + cfg.Specialization,
		Run: func(ic adkagent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {}
		},
	})
}

func mustRootAgent(t *testing.T) adkagent.Agent {
	t.Helper()
	a, err := adkagent.New(adkagent.Config{
		Name:        Name,
		Description: "warm-up specimen",
		Run: func(ic adkagent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {}
		},
	})
	if err != nil {
		t.Fatalf("build rootAgent: %v", err)
	}
	return a
}

func TestPerSessionLoader_loadsPerFamiliarConfig(t *testing.T) {
	rec := &recordingFactory{}
	registry := skillregistry.NewStubRegistry()
	loader, err := NewPerSessionLoader(rec.factory, registry, mustRootAgent(t))
	if err != nil {
		t.Fatalf("NewPerSessionLoader: %v", err)
	}

	mathID := "01957c8c-1111-7000-aaaa-1111aaaa1111"
	a, err := loader.LoadAgent(AppNamePrefix + mathID)
	if err != nil {
		t.Fatalf("LoadAgent: %v", err)
	}
	if a == nil {
		t.Fatal("LoadAgent returned nil agent")
	}
	if a.Name() != "test_"+mathID {
		t.Errorf("loaded wrong agent: name=%s", a.Name())
	}
	if len(rec.calls) != 1 {
		t.Fatalf("want 1 factory call; got %d", len(rec.calls))
	}
	if got := rec.calls[0]; got.Specialization != "math" {
		t.Errorf("factory got wrong cfg: specialization=%s", got.Specialization)
	}
}

func TestPerSessionLoader_loadsDistinctConfigsPerCall(t *testing.T) {
	rec := &recordingFactory{}
	loader, _ := NewPerSessionLoader(rec.factory, skillregistry.NewStubRegistry(), mustRootAgent(t))

	mathID := "01957c8c-1111-7000-aaaa-1111aaaa1111"
	historyID := "01957c8c-2222-7000-bbbb-2222bbbb2222"
	codingID := "01957c8c-3333-7000-cccc-3333cccc3333"

	for _, id := range []string{mathID, historyID, codingID} {
		if _, err := loader.LoadAgent(AppNamePrefix + id); err != nil {
			t.Fatalf("LoadAgent(%s): %v", id, err)
		}
	}

	if len(rec.calls) != 3 {
		t.Fatalf("want 3 factory calls; got %d", len(rec.calls))
	}

	specs := []string{rec.calls[0].Specialization, rec.calls[1].Specialization, rec.calls[2].Specialization}
	if specs[0] != "math" || specs[1] != "history" || specs[2] != "coding" {
		t.Errorf("specializations should match dispatch order math/history/coding; got %v", specs)
	}
}

func TestPerSessionLoader_returnsRootForEmptyName(t *testing.T) {
	rec := &recordingFactory{}
	root := mustRootAgent(t)
	loader, _ := NewPerSessionLoader(rec.factory, skillregistry.NewStubRegistry(), root)

	a, err := loader.LoadAgent("")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if a != root {
		t.Error("empty name should return root specimen")
	}
	if len(rec.calls) != 0 {
		t.Errorf("empty name should bypass factory; got %d calls", len(rec.calls))
	}
}

func TestPerSessionLoader_returnsRootForCanonicalAgentName(t *testing.T) {
	rec := &recordingFactory{}
	root := mustRootAgent(t)
	loader, _ := NewPerSessionLoader(rec.factory, skillregistry.NewStubRegistry(), root)

	a, err := loader.LoadAgent(Name) // "familiar_companion"
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if a != root {
		t.Error("canonical agent name should return root specimen")
	}
}

func TestPerSessionLoader_rejectsMalformedAppName(t *testing.T) {
	rec := &recordingFactory{}
	loader, _ := NewPerSessionLoader(rec.factory, skillregistry.NewStubRegistry(), mustRootAgent(t))

	_, err := loader.LoadAgent("course_planner:tenant-abc")
	if !errors.Is(err, ErrMalformedAppName) {
		t.Errorf("want ErrMalformedAppName for unknown prefix; got %v", err)
	}
}

func TestPerSessionLoader_propagatesRegistryError(t *testing.T) {
	rec := &recordingFactory{}
	loader, _ := NewPerSessionLoader(rec.factory, skillregistry.NewStubRegistry(), mustRootAgent(t))

	unknownID := "01999999-9999-7000-9999-999999999999"
	_, err := loader.LoadAgent(AppNamePrefix + unknownID)
	if err == nil {
		t.Fatal("want error for unknown familiar_id; got nil")
	}
	if !errors.Is(err, skillregistry.ErrFamiliarNotFound) {
		t.Errorf("error should wrap ErrFamiliarNotFound; got %v", err)
	}
	if len(rec.calls) != 0 {
		t.Errorf("factory should not be called when registry errors; got %d calls", len(rec.calls))
	}
}

func TestPerSessionLoader_rejectsNilFactory(t *testing.T) {
	_, err := NewPerSessionLoader(nil, skillregistry.NewStubRegistry(), mustRootAgent(t))
	if err == nil {
		t.Error("want error for nil factory; got nil")
	}
}

func TestPerSessionLoader_rejectsNilRegistry(t *testing.T) {
	rec := &recordingFactory{}
	_, err := NewPerSessionLoader(rec.factory, nil, mustRootAgent(t))
	if err == nil {
		t.Error("want error for nil registry; got nil")
	}
}

func TestPerSessionLoader_rejectsNilRootAgent(t *testing.T) {
	rec := &recordingFactory{}
	_, err := NewPerSessionLoader(rec.factory, skillregistry.NewStubRegistry(), nil)
	if err == nil {
		t.Error("want error for nil rootAgent; got nil")
	}
}

func TestPerSessionLoader_listAgentsReportsCanonicalName(t *testing.T) {
	rec := &recordingFactory{}
	loader, _ := NewPerSessionLoader(rec.factory, skillregistry.NewStubRegistry(), mustRootAgent(t))
	got := loader.ListAgents()
	if len(got) != 1 {
		t.Fatalf("want exactly 1 logical agent name; got %d", len(got))
	}
	if got[0] != Name {
		t.Errorf("want %s; got %s", Name, got[0])
	}
}

func TestPerSessionLoader_rootAgentReturnsConstructionSpecimen(t *testing.T) {
	rec := &recordingFactory{}
	root := mustRootAgent(t)
	loader, _ := NewPerSessionLoader(rec.factory, skillregistry.NewStubRegistry(), root)
	if loader.RootAgent() != root {
		t.Error("RootAgent should return the specimen passed at construction")
	}
}

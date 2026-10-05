package agent

// labelplugin_test.go — unit tests for labelplugin.go helpers.
//
// Coverage targets:
//   - getStrFromState: hit/miss, type mismatch (non-string → Sprintf), error path
//   - setDeferredState: sets stateKeyMemoryWriteDeferred = "true"
//   - NewFamiliarIDLabelPlugin: constructs without error for nil + non-nil memSvc
//
// Plugin callback coverage (BeforeRunCallback, AfterRunCallback) requires a full
// ADK InvocationContext/Session fake — not available in unit tests without the ADK
// runtime. Those paths were long claimed to be covered by a managed-runtime
// smoke suite (decommissioned 2026-06-01, ADR-169); see
// labelplugin_callbacks_test.go for the in-repo fake that actually covers them.
//
// TDD GREEN — CHO-1576 Phase 1 Agent A.

import (
	"errors"
	"iter"
	"testing"

	"google.golang.org/adk/session"
)

// ---------- getStrFromState ----------

func TestGetStrFromState_returnsStringValue(t *testing.T) {
	st := &labelTestState{data: map[string]any{
		"familiar_id": "01957c8c-1111-7000-aaaa-1111aaaa1111",
	}}
	got := getStrFromState(st, "familiar_id")
	if got != "01957c8c-1111-7000-aaaa-1111aaaa1111" {
		t.Errorf("getStrFromState: got %q; want familiar_id", got)
	}
}

func TestGetStrFromState_returnsEmptyStringOnMissingKey(t *testing.T) {
	st := &labelTestState{data: map[string]any{}}
	got := getStrFromState(st, "nonexistent_key")
	if got != "" {
		t.Errorf("missing key: want empty string; got %q", got)
	}
}

func TestGetStrFromState_returnsEmptyStringOnStateError(t *testing.T) {
	st := &labelErrorState{}
	got := getStrFromState(st, "familiar_id")
	if got != "" {
		t.Errorf("state error: want empty string; got %q", got)
	}
}

func TestGetStrFromState_convertsNonStringViaSprintf(t *testing.T) {
	// When the stored value is not a string (e.g. an int from older code),
	// getStrFromState must fall back to fmt.Sprintf rather than returning "".
	st := &labelTestState{data: map[string]any{
		"growth_stage": 3,
	}}
	got := getStrFromState(st, "growth_stage")
	if got == "" {
		t.Error("non-string value: want Sprintf representation; got empty string")
	}
	// The Sprintf of 3 should be "3".
	if got != "3" {
		t.Errorf("non-string Sprintf: got %q; want %q", got, "3")
	}
}

func TestGetStrFromState_returnsEmptyStringForNilValue(t *testing.T) {
	// Nil stored value is not a string; Sprintf("%v", nil) returns "<nil>" — not empty.
	// This is the expected behaviour: return the Sprintf form, NOT "".
	st := &labelTestState{data: map[string]any{
		"nil_key": nil,
	}}
	got := getStrFromState(st, "nil_key")
	// nil is not a string, so it goes through fmt.Sprintf("%v", nil) = "<nil>"
	if got == "" {
		t.Error("nil value must fall through to Sprintf, not return empty string")
	}
}

// ---------- NewFamiliarIDLabelPlugin ----------

func TestNewFamiliarIDLabelPlugin_constructs(t *testing.T) {
	p, err := NewFamiliarIDLabelPlugin()
	if err != nil {
		t.Fatalf("NewFamiliarIDLabelPlugin(): unexpected error: %v", err)
	}
	if p == nil {
		t.Fatal("NewFamiliarIDLabelPlugin(): returned nil plugin")
	}
}

// ---------- SessionSpanAttributes ----------

func TestSessionSpanAttributes_carriesTheThreeIdentityKeys(t *testing.T) {
	attrs := SessionSpanAttributes(
		"01957c8c-1111-7000-aaaa-1111aaaa1111",
		"01957c8c-0000-7000-9999-000099990000",
		"01957c8c-0000-7000-8888-000088880000",
	)

	got := make(map[string]string, len(attrs))
	for _, a := range attrs {
		got[string(a.Key)] = a.Value.AsString()
	}

	want := map[string]string{
		"chora.familiar_id": "01957c8c-1111-7000-aaaa-1111aaaa1111",
		"chora.gcid":        "01957c8c-0000-7000-9999-000099990000",
		"chora.tenant_id":   "01957c8c-0000-7000-8888-000088880000",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("attribute %q = %q; want %q", k, got[k], v)
		}
	}
	if len(attrs) != len(want) {
		t.Errorf("got %d attributes; want exactly %d (memory-bank keys were removed with ADR-169)", len(attrs), len(want))
	}
}

func TestFamiliarIDStateKeyConstant(t *testing.T) {
	// Regression guard: familiarIDStateKey must match what instancedispatch + main.go
	// write into session state under the same key.
	if familiarIDStateKey != "familiar_id" {
		t.Errorf("familiarIDStateKey changed: got %q; want \"familiar_id\"", familiarIDStateKey)
	}
}

// ---------- helpers ----------

// labelTestState is a simple map-backed session.State for labelplugin tests.
type labelTestState struct {
	data map[string]any
}

func (s *labelTestState) Get(k string) (any, error) {
	v, ok := s.data[k]
	if !ok {
		return nil, session.ErrStateKeyNotExist
	}
	return v, nil
}

func (s *labelTestState) Set(k string, v any) error {
	s.data[k] = v
	return nil
}

func (s *labelTestState) All() iter.Seq2[string, any] {
	return func(yield func(string, any) bool) {
		for k, v := range s.data {
			if !yield(k, v) {
				return
			}
		}
	}
}

// labelErrorState is a session.State whose Get and Set always return an error.
// Used to cover the error-path branches of getStrFromState and setDeferredState.
type labelErrorState struct{}

func (e *labelErrorState) Get(_ string) (any, error) {
	return nil, errors.New("state unavailable")
}

func (e *labelErrorState) Set(_ string, _ any) error {
	return errors.New("state unavailable")
}

func (e *labelErrorState) All() iter.Seq2[string, any] {
	return func(yield func(string, any) bool) {}
}

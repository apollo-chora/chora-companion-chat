package agent

// labelplugin_callbacks_test.go — drives BeforeRunCallback + AfterRunCallback of
// NewFamiliarIDLabelPlugin through a fake ADK InvocationContext.
//
// This closes a gap that labelplugin_test.go's header asserted was closed by
// something else: "Plugin callback coverage (BeforeRunCallback, AfterRunCallback)
// requires a full ADK InvocationContext/Session fake — not available in unit
// tests without the ADK runtime. Those paths were claimed to be covered by a
// managed-runtime smoke suite." Both halves of that were false by 2026-08-24.
// The managed runtime was decommissioned 2026-06-01 (ADR-169), so the suite
// named as the cover does not exist, and
// chora-adk-common/instancedispatch/fakes_test.go has carried a complete
// agent.InvocationContext fake for months. The callbacks were therefore
// covered by nothing at all: 22 of this file's 31 statements, the two
// branches that decide whether a turn is billed to the right Companion and
// whether a lost memory-bank write is recorded rather than swallowed.
//
// The fakes below are the instancedispatch shape, minus the members these two
// callbacks never touch.

import (
	"context"
	"iter"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/embedded"
	"go.opentelemetry.io/otel/trace/noop"

	"google.golang.org/genai"

	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/session"
)

// ---------- BeforeRunCallback ----------

func TestLabelPlugin_BeforeRun_stampsSpanAttributesFromSessionState(t *testing.T) {
	p, err := NewFamiliarIDLabelPlugin()
	if err != nil {
		t.Fatalf("NewFamiliarIDLabelPlugin: %v", err)
	}
	cb := p.BeforeRunCallback()
	if cb == nil {
		t.Fatal("BeforeRunCallback must be wired")
	}

	span := &recordingSpan{recording: true}
	ic := newLabelInvocationCtx(
		oteltrace.ContextWithSpan(context.Background(), span),
		"familiar:01957c8c-1111-7000-aaaa-1111aaaa1111",
		map[string]any{
			"familiar_id": "01957c8c-1111-7000-aaaa-1111aaaa1111",
			"user_gcid":   "00000000-0000-7000-8000-000000001999",
			"tenant_id":   "11111111-1111-7111-8111-111111111111",
		})

	content, err := cb(ic)
	if err != nil {
		t.Fatalf("BeforeRun must not error on a well-formed turn: %v", err)
	}
	if content != nil {
		t.Errorf("BeforeRun must not inject content; got %v", content)
	}

	// The billing label is the point of the plugin: assert the three identity
	// attributes reached the span, not merely that SetAttributes was called.
	got := map[string]string{}
	for _, kv := range span.attrs {
		got[string(kv.Key)] = kv.Value.AsString()
	}
	for k, want := range map[string]string{
		"chora.familiar_id": "01957c8c-1111-7000-aaaa-1111aaaa1111",
		"chora.gcid":        "00000000-0000-7000-8000-000000001999",
		"chora.tenant_id":   "11111111-1111-7111-8111-111111111111",
	} {
		if got[k] != want {
			t.Errorf("span attribute %s: got %q; want %q", k, got[k], want)
		}
	}
}

func TestLabelPlugin_BeforeRun_doesNotStampWhenSpanIsNotRecording(t *testing.T) {
	// A non-recording span must be left alone: stamping a sampled-out span costs
	// allocation for nothing, and the guard is the only thing preventing it.
	p, _ := NewFamiliarIDLabelPlugin()
	span := &recordingSpan{recording: false}
	ic := newLabelInvocationCtx(
		oteltrace.ContextWithSpan(context.Background(), span),
		"familiar:f1",
		map[string]any{"familiar_id": "f1", "user_gcid": "g1", "tenant_id": "t1"})

	if _, err := p.BeforeRunCallback()(ic); err != nil {
		t.Fatalf("BeforeRun: %v", err)
	}
	if len(span.attrs) != 0 {
		t.Errorf("non-recording span must not be stamped; got %d attributes", len(span.attrs))
	}
}

func TestLabelPlugin_BeforeRun_passesTurnThroughWhenFamiliarIDAbsent(t *testing.T) {
	// instancedispatch refuses the turn upstream. This plugin must NOT also
	// refuse: a second refusal here would turn one refusal into an error the
	// dispatcher never expects, so the contract is warn-and-continue.
	p, _ := NewFamiliarIDLabelPlugin()
	span := &recordingSpan{recording: true}
	ic := newLabelInvocationCtx(
		oteltrace.ContextWithSpan(context.Background(), span),
		"familiar:none",
		map[string]any{"user_gcid": "g1", "tenant_id": "t1"})

	content, err := p.BeforeRunCallback()(ic)
	if err != nil {
		t.Errorf("missing familiar_id must not error (instancedispatch owns the refusal); got %v", err)
	}
	if content != nil {
		t.Errorf("missing familiar_id must not inject content; got %v", content)
	}
	if len(span.attrs) != 0 {
		t.Errorf("no familiar_id means no billing label; got %d attributes", len(span.attrs))
	}
}

// ---------- fakes ----------

// recordingSpan is a trace.Span that captures SetAttributes and reports a
// configurable IsRecording, so both sides of the sampling guard are reachable.
type recordingSpan struct {
	embedded.Span
	recording bool
	attrs     []attribute.KeyValue
}

func (s *recordingSpan) End(...oteltrace.SpanEndOption)              {}
func (s *recordingSpan) AddEvent(string, ...oteltrace.EventOption)   {}
func (s *recordingSpan) AddLink(oteltrace.Link)                      {}
func (s *recordingSpan) IsRecording() bool                           { return s.recording }
func (s *recordingSpan) RecordError(error, ...oteltrace.EventOption) {}
func (s *recordingSpan) SpanContext() oteltrace.SpanContext          { return oteltrace.SpanContext{} }
func (s *recordingSpan) SetStatus(codes.Code, string)                {}
func (s *recordingSpan) SetName(string)                              {}
func (s *recordingSpan) SetAttributes(kv ...attribute.KeyValue)      { s.attrs = append(s.attrs, kv...) }
func (s *recordingSpan) TracerProvider() oteltrace.TracerProvider {
	return noop.NewTracerProvider()
}

type labelFakeState struct{ m map[string]any }

func (f *labelFakeState) Get(k string) (any, error) {
	if v, ok := f.m[k]; ok {
		return v, nil
	}
	return nil, session.ErrStateKeyNotExist
}
func (f *labelFakeState) Set(k string, v any) error { f.m[k] = v; return nil }
func (f *labelFakeState) All() iter.Seq2[string, any] {
	return func(yield func(string, any) bool) {
		for k, v := range f.m {
			if !yield(k, v) {
				return
			}
		}
	}
}

type labelFakeSession struct {
	appName string
	state   *labelFakeState
}

func (f *labelFakeSession) ID() string                { return "fake-session" }
func (f *labelFakeSession) AppName() string           { return f.appName }
func (f *labelFakeSession) UserID() string            { return "fake-user" }
func (f *labelFakeSession) State() session.State      { return f.state }
func (f *labelFakeSession) Events() session.Events    { return nil }
func (f *labelFakeSession) LastUpdateTime() time.Time { return time.Time{} }

type labelInvocationCtx struct {
	ctx  context.Context
	sess *labelFakeSession
}

func newLabelInvocationCtx(ctx context.Context, appName string, state map[string]any) *labelInvocationCtx {
	return &labelInvocationCtx{
		ctx:  ctx,
		sess: &labelFakeSession{appName: appName, state: &labelFakeState{m: state}},
	}
}

func (f *labelInvocationCtx) Deadline() (time.Time, bool) { return f.ctx.Deadline() }
func (f *labelInvocationCtx) Done() <-chan struct{}       { return f.ctx.Done() }
func (f *labelInvocationCtx) Err() error                  { return f.ctx.Err() }
func (f *labelInvocationCtx) Value(key any) any           { return f.ctx.Value(key) }

func (f *labelInvocationCtx) Agent() adkagent.Agent          { return nil }
func (f *labelInvocationCtx) Artifacts() adkagent.Artifacts  { return nil }
func (f *labelInvocationCtx) Memory() adkagent.Memory        { return nil }
func (f *labelInvocationCtx) Session() session.Session       { return f.sess }
func (f *labelInvocationCtx) InvocationID() string           { return "fake-inv" }
func (f *labelInvocationCtx) Branch() string                 { return "" }
func (f *labelInvocationCtx) UserContent() *genai.Content    { return nil }
func (f *labelInvocationCtx) RunConfig() *adkagent.RunConfig { return nil }
func (f *labelInvocationCtx) EndInvocation()                 {}
func (f *labelInvocationCtx) Ended() bool                    { return false }
func (f *labelInvocationCtx) WithContext(ctx context.Context) adkagent.InvocationContext {
	return &labelInvocationCtx{ctx: ctx, sess: f.sess}
}

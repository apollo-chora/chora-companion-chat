package boot

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"net"
	"strings"
	"testing"

	"google.golang.org/adk/agent"
	adkmodel "google.golang.org/adk/model"
	"google.golang.org/adk/plugin"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"

	"github.com/apollo-chora/chora-adk-common/agentdispatch"
	"github.com/apollo-chora/chora-adk-common/modelgatewayclient"
	capp "github.com/apollo-chora/chora-companion-chat/internal/agent"
	"github.com/apollo-chora/chora-companion-chat/internal/skillregistry"
	tools "github.com/apollo-chora/chora-companion-chat/internal/tool"
)

func minimalEnv(t *testing.T) {
	t.Setenv(EnvProject, "chora-test")
	t.Setenv("CHORA_GATEWAY_TENANT_ID", "11111111-1111-7111-8111-111111111111")
	t.Setenv("CHORA_GATEWAY_GCID", "00000000-0000-7000-8000-000000001999")
	t.Setenv(EnvSessionsDSNSecret, "chora-dev-pg-chora_ai_kernel-app_rw-dsn")
	t.Setenv(EnvModel, "")
	t.Setenv(EnvSessionsSchema, "")
	t.Setenv("CONSUMPTION_GRPC_ENDPOINT", "stub://chora-consumption")
	t.Setenv("CREATION_GRPC_ENDPOINT", "stub://chora-creation")
}

// --- config + identity -------------------------------------------------------

func TestLoadConfig_guards(t *testing.T) {
	minimalEnv(t)
	t.Setenv(EnvSessionsDSNSecret, "")
	if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), EnvSessionsDSNSecret) {
		t.Errorf("a chat agent must refuse to start without its Postgres session store secret: %v", err)
	}
	minimalEnv(t)
	t.Setenv(EnvProject, "")
	cfg, err := LoadConfig()
	if err != nil || cfg.ProjectID != "chora-local" {
		t.Errorf("project default: %+v %v", cfg, err)
	}
	minimalEnv(t)
	cfg, err = LoadConfig()
	if err != nil || cfg.ProjectID != "chora-test" || cfg.Model != "gemini-2.5-flash" || cfg.SessionsSchema != "companion_chat_sessions" || cfg.SessionsDSNSecretID != "chora-dev-pg-chora_ai_kernel-app_rw-dsn" {
		t.Errorf("defaults: %+v %v", cfg, err)
	}
	if strings.Contains(strings.Join(func() []string {
		var s []string
		for _, a := range cfg.LogAttrs() {
			if str, ok := a.(string); ok {
				s = append(s, str)
			}
		}
		return s
	}(), " "), "postgres://") {
		t.Errorf("LogAttrs must never carry the DSN")
	}
	gw := gatewayConfig(cfg)
	if gw.AgentID != "companion_chat" || gw.Surface != "companion_chat" || gw.CrewKind != "companion_chat" {
		t.Errorf("gateway identity = %+v", gw)
	}
	if capp.Name != "companion_chat" {
		t.Errorf("the ADK agent name must be companion_chat (ADR-254 D9), got %q", capp.Name)
	}
}

func TestSessionKeyAndUserMessage(t *testing.T) {
	typed := agentdispatch.Request{TenantID: "t1", GCID: "g1", InputPayload: `{"conversation_id":"conv-9","message":"  hello there ","turn_kind":"typed"}`}
	u, s, ok := SessionKey(typed)
	if !ok || u != "t1:g1" || s != "conv-9" {
		t.Errorf("typed key = %q %q %v", u, s, ok)
	}
	if UserMessage(typed) != "hello there" {
		t.Errorf("typed user message = %q", UserMessage(typed))
	}
	reflect := agentdispatch.Request{TenantID: "t1", GCID: "g1", InputPayload: `{"turn_kind":"reflect","reflection_json":"{}","message":"ignored"}`}
	if _, _, ok := SessionKey(reflect); ok {
		t.Errorf("a reflect turn has no conversation and must run stateless")
	}
	if UserMessage(reflect) != "" {
		t.Errorf("non-typed turns run on the BEGIN trigger, got %q", UserMessage(reflect))
	}
	voice := agentdispatch.Request{TenantID: "t1", GCID: "g1", InputPayload: `{"turn_kind":"voice","conversation_id":"conv-9","diagnosis_json":"{}"}`}
	if _, s, ok := SessionKey(voice); !ok || s != "conv-9" {
		t.Errorf("voice turns join the conversation: %q %v", s, ok)
	}
	if UserMessage(voice) != VoiceTrigger || !strings.Contains(VoiceTrigger, "Growth-Edge diagnosis") {
		t.Errorf("a voice turn's user event must describe itself in the dialogue, got %q", UserMessage(voice))
	}
}

func TestWithSearchPath(t *testing.T) {
	got, err := WithSearchPath("postgres://u:p@127.0.0.1:5432/chora_ai_kernel?sslmode=disable", "companion_chat_sessions")
	if err != nil || !strings.Contains(got, "search_path=companion_chat_sessions") || !strings.Contains(got, "sslmode=disable") || !strings.Contains(got, "u:p@127.0.0.1:5432/chora_ai_kernel") {
		t.Errorf("got %q %v", got, err)
	}
	if _, err := WithSearchPath("postgres://u:p@h/db?search_path=public", "x"); err == nil {
		t.Errorf("an already pinned search_path must be refused")
	}
	if _, err := WithSearchPath("mysql://u:p@h/db", "x"); err == nil {
		t.Errorf("non-postgres DSN must be refused")
	}
	if _, err := WithSearchPath("postgres://u:p@h/db", ""); err == nil {
		t.Errorf("empty schema must be refused")
	}
}

type fakeSecrets struct{ values map[string]string }

func (f fakeSecrets) GetSecret(_ context.Context, name string) (string, error) {
	v, ok := f.values[name]
	if !ok {
		return "", errors.New("not found")
	}
	return v, nil
}

func TestResolveSessionsDSN(t *testing.T) {
	dsn, err := ResolveSessionsDSN(context.Background(), fakeSecrets{map[string]string{"s1": "postgres://u:p@127.0.0.1:5432/chora_ai_kernel?sslmode=disable\n"}}, "s1", "companion_chat_sessions")
	if err != nil || !strings.Contains(dsn, "search_path=companion_chat_sessions") {
		t.Errorf("resolve: %q %v", dsn, err)
	}
	if _, err := ResolveSessionsDSN(context.Background(), fakeSecrets{}, "missing", "x"); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Errorf("unreadable secret must fail naming the id: %v", err)
	}
}

// --- tree + runner ------------------------------------------------------------

type scriptedLLM struct {
	answers []string
	calls   []*adkmodel.LLMRequest
}

func (s *scriptedLLM) Name() string { return "scripted" }
func (s *scriptedLLM) GenerateContent(_ context.Context, req *adkmodel.LLMRequest, _ bool) iter.Seq2[*adkmodel.LLMResponse, error] {
	return func(yield func(*adkmodel.LLMResponse, error) bool) {
		s.calls = append(s.calls, req)
		if len(s.answers) == 0 {
			yield(nil, errors.New("scriptedLLM: no answer scripted"))
			return
		}
		text := s.answers[0]
		s.answers = s.answers[1:]
		yield(&adkmodel.LLMResponse{Content: &genai.Content{Role: "model", Parts: []*genai.Part{{Text: text}}}, ModelVersion: "gemini-2.5-flash-001"}, nil)
	}
}

func systemInstruction(req *adkmodel.LLMRequest) string {
	if req == nil || req.Config == nil || req.Config.SystemInstruction == nil {
		return ""
	}
	var b strings.Builder
	for _, p := range req.Config.SystemInstruction.Parts {
		if p != nil {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

func newTree(t *testing.T, llm adkmodel.LLM) Tree {
	t.Helper()
	tree, err := BuildTree(Config{}, Deps{LLM: llm, Registry: skillregistry.NewStubRegistry(), CiteValidator: tools.StubValidator{}})
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

func runTree(t *testing.T, tree Tree, state map[string]any, userText string) (string, error) {
	return runTreeWith(t, tree, state, userText, nil)
}

// runTreeWith runs the root agent through the real runner, optionally under
// the production plugin chain, over a fresh session seeded with state.
func runTreeWith(t *testing.T, tree Tree, state map[string]any, userText string, plugins []*plugin.Plugin) (string, error) {
	t.Helper()
	ctx := context.Background()
	svc := session.InMemoryService()
	const user, sid = "11111111-1111-7111-8111-111111111111:g1", "conv-1"
	if _, err := svc.Create(ctx, &session.CreateRequest{AppName: SessionAppName, UserID: user, SessionID: sid, State: state}); err != nil {
		t.Fatal(err)
	}
	r, err := runner.New(runner.Config{AppName: SessionAppName, Agent: tree.Root, SessionService: svc, PluginConfig: runner.PluginConfig{Plugins: plugins}})
	if err != nil {
		t.Fatal(err)
	}
	if userText == "" {
		userText = "BEGIN"
	}
	var events []*session.Event
	for ev, err := range r.Run(ctx, user, sid, &genai.Content{Role: "user", Parts: []*genai.Part{{Text: userText}}}, agent.RunConfig{}) {
		if err != nil {
			return "", err
		}
		events = append(events, ev)
	}
	return agentdispatch.TerminalText(events, agentdispatch.TerminalAuthor(DispatchRole))
}

func stubCompanionState(extra map[string]any) map[string]any {
	st := map[string]any{"tenant_id": "t1", "user_gcid": "g1", "familiar_id": "01957c8c-1111-7000-aaaa-1111aaaa1111", "mana_tier": "standard"}
	for k, v := range extra {
		st[k] = v
	}
	return st
}

func TestTree_shapeAndTools(t *testing.T) {
	tree := newTree(t, &scriptedLLM{})
	if tree.Root.Name() != routerAgentName || len(tree.Root.SubAgents()) != 1 || tree.Root.SubAgents()[0].Name() != "companion_chat" {
		t.Errorf("tree = %v", tree.Root)
	}
	if len(tree.Tools) != 1 || tree.Tools[0].Name() != "cite_atom" {
		t.Errorf("with a stub consumption endpoint only cite_atom ships, got %d tools", len(tree.Tools))
	}
	withReader, err := BuildTree(Config{}, Deps{LLM: &scriptedLLM{}, Registry: skillregistry.NewStubRegistry(), CiteValidator: tools.StubValidator{}, WeaknessReader: fakeReader{}})
	if err != nil || len(withReader.Tools) != 2 || withReader.Tools[1].Name() != "weakness_read" {
		t.Errorf("with a reader the weakness_read tool ships: %v %v", withReader.Tools, err)
	}
}

type fakeReader struct{}

func (fakeReader) ReadWeakness(context.Context, string, string, string, int) ([]tools.WeaknessEdgeView, error) {
	return nil, nil
}

func TestTree_typedTurnAnswersWithTheEnvelope(t *testing.T) {
	llm := &scriptedLLM{answers: []string{"Hi! Ready to practise fractions?"}}
	tree := newTree(t, llm)
	_, err := runTree(t, tree, stubCompanionState(nil), "hello")
	var perm *agentdispatch.PermanentError
	if !errors.As(err, &perm) || !strings.Contains(err.Error(), "missing_message") || len(llm.calls) != 0 {
		t.Fatalf("a typed turn without message must be a permanent refusal before any model call: %v calls=%d", err, len(llm.calls))
	}
	out, err := runTree(t, tree, stubCompanionState(map[string]any{"message": "hello"}), "hello")
	if err != nil {
		t.Fatal(err)
	}
	var env capp.Envelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("terminal text is not the envelope: %v %q", err, out)
	}
	if env.TurnKind != "typed" || env.ReplyText != "Hi! Ready to practise fractions?" || env.ModelID != "gemini-2.5-flash-001" || env.PromptSource != capp.PromptSourceEmbedded || env.PromptVersion != capp.FamiliarPromptVersion {
		t.Errorf("envelope = %+v", env)
	}
	if len(llm.calls) != 1 || !strings.Contains(systemInstruction(llm.calls[0]), "Familiar") {
		t.Errorf("typed turn must run the per-Companion prompt: calls=%d", len(llm.calls))
	}
	var lastUser string
	for _, c := range llm.calls[0].Contents {
		if c.Role == "user" && len(c.Parts) > 0 {
			lastUser = c.Parts[len(c.Parts)-1].Text
		}
	}
	if lastUser != "hello" {
		t.Errorf("the learner's text must be the user turn, got %q", lastUser)
	}
}

func TestTree_reflectTurnHonoursTheLockedContract(t *testing.T) {
	reflection := `{"companion_name":"Ember","goal_title":"Fractions","concepts_total":10,"concepts_mastered":4,"shaky_concepts":[{"concept_label":"Comparing fractions","strength":0.7}],"memories":[{"content":"you caught your own slip on 3/6","created_at":"2026-08-20T10:00:00Z"}]}`
	llm := &scriptedLLM{answers: []string{"\"I remember you catching your own slip on 3/6; comparing fractions is still the shaky bit, and it is what this goal turns on.\""}}
	tree := newTree(t, llm)
	out, err := runTree(t, tree, stubCompanionState(map[string]any{"turn_kind": "reflect", "reflection_json": reflection, "prompt_overrides_json": `{"x":"y"}`}), "")
	if err != nil {
		t.Fatal(err)
	}
	var env capp.Envelope
	_ = json.Unmarshal([]byte(out), &env)
	if env.TurnKind != "reflect" || env.NothingToSay || !strings.HasPrefix(env.ReplyText, "I remember you") || env.PromptVersion != capp.ReflectionPromptVersion || env.PromptSource != capp.PromptSourceRegistry {
		t.Errorf("reflect envelope = %+v", env)
	}
	if si := systemInstruction(llm.calls[0]); !strings.Contains(si, "You are Ember") || !strings.Contains(si, "- Comparing fractions") || !strings.Contains(si, "NO_MEMORY_YET") {
		t.Errorf("reflection prompt: %q", si[:min(len(si), 200)])
	}

	// The honest decline travels as nothing_to_say, never as an empty reflection.
	llm = &scriptedLLM{answers: []string{"NO_MEMORY_YET"}}
	tree = newTree(t, llm)
	out, err = runTree(t, tree, stubCompanionState(map[string]any{"turn_kind": "reflect", "reflection_json": reflection}), "")
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal([]byte(out), &env)
	if !env.NothingToSay || env.ReplyText != "" {
		t.Errorf("decline envelope = %+v", env)
	}

	// An oversize reflection is refused (transient, not a FAILED on the wire).
	llm = &scriptedLLM{answers: []string{strings.Repeat("x", 601)}}
	tree = newTree(t, llm)
	_, err = runTree(t, tree, stubCompanionState(map[string]any{"turn_kind": "reflect", "reflection_json": reflection}), "")
	var perm *agentdispatch.PermanentError
	if err == nil || !errors.Is(err, capp.ErrSynthesisRefused) || errors.As(err, &perm) {
		t.Errorf("oversize must be a transient synthesis_refused, got %v", err)
	}
}

func TestTree_voiceTurnAndUnknownKind(t *testing.T) {
	llm := &scriptedLLM{answers: []string{"I had a look at your upload, and here is where we can grow next: tricky multiplication facts."}}
	tree := newTree(t, llm)
	out, err := runTree(t, tree, stubCompanionState(map[string]any{"turn_kind": "voice", "companion_name": "Ember", "locale": "en-SG",
		"diagnosis_json": `{"edges":[{"concept_label":"Multiplication facts","summary":"7x8 and 9x6 swap","suggested_angles":["arrays"]}]}`}), "")
	if err != nil {
		t.Fatal(err)
	}
	var env capp.Envelope
	_ = json.Unmarshal([]byte(out), &env)
	if env.TurnKind != "voice" || !strings.Contains(env.ReplyText, "multiplication") || env.PromptVersion != capp.VoicePromptVersion {
		t.Errorf("voice envelope = %+v", env)
	}
	if si := systemInstruction(llm.calls[0]); !strings.Contains(si, "Multiplication facts: 7x8 and 9x6 swap") {
		t.Errorf("voice prompt: %q", si[:min(len(si), 200)])
	}
	llm = &scriptedLLM{answers: []string{"never"}}
	tree = newTree(t, llm)
	_, err = runTree(t, tree, stubCompanionState(map[string]any{"turn_kind": "sing"}), "")
	var perm *agentdispatch.PermanentError
	if !errors.As(err, &perm) || !strings.Contains(err.Error(), "unknown_turn_kind: sing") || len(llm.calls) != 0 {
		t.Errorf("unknown turn kind must be permanent with no model call: %v calls=%d", err, len(llm.calls))
	}
}

func TestNewPlugins_chain(t *testing.T) {
	tree := newTree(t, &scriptedLLM{})
	ps, err := NewPlugins(tree.DispatchCfg)
	if err != nil || len(ps) != 8 {
		t.Fatalf("chain: %d %v", len(ps), err)
	}
	if !strings.Contains(ps[0].Name(), "inbound_trace") || !strings.Contains(ps[1].Name(), "seed_validation") || !strings.Contains(ps[2].Name(), "tenant_propagation") {
		t.Errorf("chain order: %q %q %q", ps[0].Name(), ps[1].Name(), ps[2].Name())
	}
	tc := TerminationConfig()
	if tc.AgentID != "companion_chat" || tc.CrewKind != "companion_chat" || tc.MaxIterations != 3 {
		t.Errorf("termination = %+v", tc)
	}
}

func TestRun_refusesArgsAndComposesTheLane(t *testing.T) {
	minimalEnv(t)
	if err := Run(context.Background(), []string{"web", "-port", "8080", "agentengine"}); err == nil || !strings.Contains(err.Error(), "takes no arguments") {
		t.Errorf("args refusal: %v", err)
	}
	oldT, oldG, oldS, oldO, oldServe := initTracing, newGatewayLLM, newSecretsClient, openSessionStore, serveSubscriber
	t.Cleanup(func() {
		initTracing, newGatewayLLM, newSecretsClient, openSessionStore, serveSubscriber = oldT, oldG, oldS, oldO, oldServe
	})
	initTracing = func(context.Context, string) (func(context.Context) error, error) {
		return func(context.Context) error { return nil }, nil
	}
	newGatewayLLM = func(context.Context, Config) (adkmodel.LLM, error) { return &scriptedLLM{}, nil }
	newSecretsClient = func(context.Context, string) (SecretReader, func() error, error) {
		return fakeSecrets{map[string]string{"chora-dev-pg-chora_ai_kernel-app_rw-dsn": "postgres://u:p@127.0.0.1:5432/chora_ai_kernel?sslmode=disable"}}, func() error { return nil }, nil
	}
	var openedDSN string
	openSessionStore = func(_ context.Context, dsn, schema string) (session.Service, error) {
		openedDSN = dsn + " schema=" + schema
		return session.InMemoryService(), nil
	}
	var got agentdispatch.ServeConfig
	errStop := errors.New("stopped")
	serveSubscriber = func(_ context.Context, cfg agentdispatch.ServeConfig, _ agentdispatch.RunOptions) error {
		got = cfg
		return errStop
	}
	if err := Run(context.Background(), nil); !errors.Is(err, errStop) {
		t.Fatalf("want the subscriber error to surface, got %v", err)
	}
	if !strings.Contains(openedDSN, "search_path=companion_chat_sessions") || !strings.Contains(openedDSN, "schema=companion_chat_sessions") {
		t.Errorf("session store must open on the pinned schema: %q", openedDSN)
	}
	if got.AgentRole != "companion_chat" || got.ServiceName != "chora-companion-chat" || got.AppName != "companion_chat" || got.SessionKey == nil || got.UserMessage == nil || len(got.Plugins.Plugins) != 8 || got.RootAgent == nil {
		t.Errorf("serve config = role %q service %q app %q sessionkey=%v usermsg=%v plugins=%d", got.AgentRole, got.ServiceName, got.AppName, got.SessionKey != nil, got.UserMessage != nil, len(got.Plugins.Plugins))
	}
	// A session store that cannot open is fatal before serving.
	openSessionStore = func(context.Context, string, string) (session.Service, error) { return nil, errors.New("no db") }
	served := false
	serveSubscriber = func(context.Context, agentdispatch.ServeConfig, agentdispatch.RunOptions) error {
		served = true
		return nil
	}
	if err := Run(context.Background(), nil); err == nil || served {
		t.Errorf("a chat agent without its session store must not serve: err=%v served=%v", err, served)
	}
}

func TestOpenSessionStore_unreachableIsFatal(t *testing.T) {
	// A store that cannot open (or cannot prove its migrated shape) must fail
	// the boot, never fall back to memory or to a tenant-blind store.
	if _, err := OpenSessionStore(context.Background(), "postgres://u:p@127.0.0.1:1/chora_ai_kernel?sslmode=disable&connect_timeout=1", "companion_chat_sessions"); err == nil {
		t.Fatal("OpenSessionStore against a closed port returned no error")
	}
	for _, bad := range []string{"", "nocolon", ":gcid", "not-a-uuid:gcid"} {
		if _, err := TenantFromUserID(bad); err == nil {
			t.Errorf("TenantFromUserID(%q) must refuse", bad)
		}
	}
	if tenant, err := TenantFromUserID("11111111-1111-7111-8111-111111111111:00000000-0000-7000-8000-000000001999"); err != nil || tenant != "11111111-1111-7111-8111-111111111111" {
		t.Errorf("TenantFromUserID = %q %v", tenant, err)
	}
	if _, _, err := newSecretReader(context.Background(), ""); err == nil {
		t.Fatal("a secrets client without a project must fail")
	}
}

func TestLogAttrs_truncatesGCID(t *testing.T) {
	// The boot line must name the location but never carry a full GCID.
	attrs := Config{Location: "us-central1", GatewayGCID: "0190a1b2-c3d4"}.LogAttrs()
	joined := ""
	for _, a := range attrs {
		if s, ok := a.(string); ok {
			joined += s + " "
		}
	}
	if !strings.Contains(joined, "us-central1") || !strings.Contains(joined, "0190a1b2...") || strings.Contains(joined, "0190a1b2-c3d4") {
		t.Errorf("LogAttrs = %s", joined)
	}
}

func TestPluginChain_turnAwareInstanceDispatch(t *testing.T) {
	fixture := skillregistry.NewStubRegistry()
	cfgObj, err := fixture.LoadFamiliarConfig(context.Background(), "01957c8c-1111-7000-aaaa-1111aaaa1111")
	if err != nil {
		t.Fatal(err)
	}
	_ = cfgObj
	// voice needs familiar_id but NOT familiar_config: runs, one model call, no tools offered.
	llm := &scriptedLLM{answers: []string{"Here is where we can grow next: multiplication facts."}}
	tree := newTree(t, llm)
	plugins, err := NewPlugins(tree.DispatchCfg)
	if err != nil {
		t.Fatal(err)
	}
	out, err := runTreeWith(t, tree, map[string]any{"tenant_id": "11111111-1111-7111-8111-111111111111", "user_gcid": "g1",
		"familiar_id": "01957c8c-1111-7000-aaaa-1111aaaa1111", "turn_kind": "voice", "companion_name": "Newton",
		"diagnosis_json": `{"edges":[{"concept_label":"Multiplication facts","summary":"7x8 and 9x6 swap"}]}`}, "", plugins)
	if err != nil {
		t.Fatalf("voice without familiar_config must run: %v", err)
	}
	if !strings.Contains(out, `"turn_kind":"voice"`) || len(llm.calls) != 1 || len(llm.calls[0].Tools) != 0 {
		t.Errorf("voice: out=%s calls=%d tools=%d", out, len(llm.calls), len(llm.calls[0].Tools))
	}
	// typed WITHOUT familiar_config is refused by name before any model call.
	llm = &scriptedLLM{answers: []string{"never"}}
	tree = newTree(t, llm)
	plugins, _ = NewPlugins(tree.DispatchCfg)
	_, err = runTreeWith(t, tree, map[string]any{"tenant_id": "11111111-1111-7111-8111-111111111111", "user_gcid": "g1",
		"familiar_id": "01957c8c-1111-7000-aaaa-1111aaaa1111", "turn_kind": "typed", "message": "hi"}, "hi", plugins)
	var perm *agentdispatch.PermanentError
	if !errors.As(err, &perm) || !strings.Contains(err.Error(), "missing_familiar_config") || len(llm.calls) != 0 {
		t.Errorf("typed without config: err=%v calls=%d", err, len(llm.calls))
	}
	// any turn WITHOUT familiar_id is refused by name.
	_, err = runTreeWith(t, tree, map[string]any{"tenant_id": "11111111-1111-7111-8111-111111111111", "user_gcid": "g1", "turn_kind": "reflect", "reflection_json": "{}"}, "", plugins)
	if !errors.As(err, &perm) || !strings.Contains(err.Error(), "missing_familiar_id") {
		t.Errorf("missing familiar_id: %v", err)
	}
}

func TestPluginChain_seedValidationRefusesAStringGrowthStage(t *testing.T) {
	// A growth_stage the wire carries as a string can never succeed on a
	// redelivery: refused by name, permanent, before any model call.
	llm := &scriptedLLM{answers: []string{"never"}}
	tree := newTree(t, llm)
	plugins, err := NewPlugins(tree.DispatchCfg)
	if err != nil {
		t.Fatal(err)
	}
	_, err = runTreeWith(t, tree, map[string]any{"tenant_id": "11111111-1111-7111-8111-111111111111", "user_gcid": "g1",
		"familiar_id": "01957c8c-1111-7000-aaaa-1111aaaa1111", "turn_kind": "voice", "companion_name": "Newton",
		"growth_stage": "3", "diagnosis_json": `{"edges":[{"concept_label":"Multiplication facts","summary":"7x8 and 9x6 swap"}]}`}, "", plugins)
	var perm *agentdispatch.PermanentError
	if !errors.As(err, &perm) || !strings.Contains(err.Error(), "invalid_growth_stage") || len(llm.calls) != 0 {
		t.Fatalf("string growth_stage: err=%v calls=%d", err, len(llm.calls))
	}
	// The numeric form the producers send (a JSON number decodes as float64) runs.
	llm = &scriptedLLM{answers: []string{"Here is where we can grow next: multiplication facts."}}
	tree = newTree(t, llm)
	plugins, _ = NewPlugins(tree.DispatchCfg)
	out, err := runTreeWith(t, tree, map[string]any{"tenant_id": "11111111-1111-7111-8111-111111111111", "user_gcid": "g1",
		"familiar_id": "01957c8c-1111-7000-aaaa-1111aaaa1111", "turn_kind": "voice", "companion_name": "Newton",
		"growth_stage": float64(3), "diagnosis_json": `{"edges":[{"concept_label":"Multiplication facts","summary":"7x8 and 9x6 swap"}]}`}, "", plugins)
	if err != nil || !strings.Contains(out, `"turn_kind":"voice"`) {
		t.Fatalf("numeric growth_stage must run: out=%s err=%v", out, err)
	}
}

func TestOpenSessionStoreWithDialRetry(t *testing.T) {
	old := openSessionStore
	t.Cleanup(func() { openSessionStore = old })
	// A dial fault (the proxy sidecar not listening yet) is retried until the
	// store answers; the boot then proceeds with that store.
	calls := 0
	want := session.InMemoryService()
	openSessionStore = func(context.Context, string, string) (session.Service, error) {
		calls++
		if calls < 3 {
			return nil, errors.New("companion_chat: sessions shape check: failed to connect to `user=x database=y`: 127.0.0.1:5432 (127.0.0.1): dial error: dial tcp 127.0.0.1:5432: connect: connection refused")
		}
		return want, nil
	}
	got, err := openSessionStoreWithDialRetry(context.Background(), "dsn", "schema")
	if err != nil || got != want || calls != 3 {
		t.Fatalf("retry: err=%v calls=%d", err, calls)
	}
	// A shape fault is permanent: no retry, the error surfaces at once.
	calls = 0
	openSessionStore = func(context.Context, string, string) (session.Service, error) {
		calls++
		return nil, errors.New("companion_chat: sessions store is not in the migrated shape (apply migration 0058): events: no tenant_id column")
	}
	if _, err := openSessionStoreWithDialRetry(context.Background(), "dsn", "schema"); err == nil || calls != 1 {
		t.Fatalf("shape fault must not retry: err=%v calls=%d", err, calls)
	}
	// A cancelled context ends the wait with the cause.
	calls = 0
	openSessionStore = func(context.Context, string, string) (session.Service, error) {
		calls++
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := openSessionStoreWithDialRetry(ctx, "dsn", "schema"); err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("cancel must surface: %v", err)
	}
	if !isDialFault(&net.OpError{Op: "dial"}) || isDialFault(errors.New("42501 permission denied")) || isDialFault(nil) {
		t.Fatal("isDialFault classification wrong")
	}
}

// W3 (ADR-254 D7): consumption keeps stamping the legacy familiar_chat_turn_{tier}
// into mana_action_code until its W4 cut; from the W3 hour the chat agent bills
// under the companion_* twins (identity 0039), so the resolver translates the
// chat family and passes every other value through untouched.
func TestCompanionActionCode(t *testing.T) {
	cases := map[string]string{
		"familiar_chat_turn_basic":     "companion_chat_turn_basic",
		"familiar_chat_turn_standard":  "companion_chat_turn_standard",
		"familiar_chat_turn_premium":   "companion_chat_turn_premium",
		"familiar_chat":                "companion_chat",
		" familiar_chat_turn_basic ":   "companion_chat_turn_basic",
		"companion_chat_turn_standard": "companion_chat_turn_standard",
		"companion_chat":               "companion_chat",
		"familiar_explain_anew":        "familiar_explain_anew",
		"":                             "",
	}
	for in, want := range cases {
		if got := companionActionCode(in); got != want {
			t.Errorf("companionActionCode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPluginChain_actionCodeRidesAsTheCompanionTwin(t *testing.T) {
	base := map[string]any{"tenant_id": "11111111-1111-7111-8111-111111111111", "user_gcid": "g1",
		"familiar_id": "01957c8c-1111-7000-aaaa-1111aaaa1111", "turn_kind": "voice", "companion_name": "Newton",
		"diagnosis_json": `{"edges":[{"concept_label":"Multiplication facts","summary":"7x8 and 9x6 swap"}]}`}
	for stamped, want := range map[string]string{
		"familiar_chat_turn_basic":    "companion_chat_turn_basic",
		"companion_chat_turn_premium": "companion_chat_turn_premium",
		"":                            "",
	} {
		llm := &scriptedLLM{answers: []string{"Here is where we can grow next: multiplication facts."}}
		tree := newTree(t, llm)
		plugins, err := NewPlugins(tree.DispatchCfg)
		if err != nil {
			t.Fatal(err)
		}
		state := map[string]any{}
		for k, v := range base {
			state[k] = v
		}
		if stamped != "" {
			state[stateKeyManaActionCode] = stamped
		}
		if _, err := runTreeWith(t, tree, state, "", plugins); err != nil {
			t.Fatalf("stamped %q: %v", stamped, err)
		}
		if len(llm.calls) != 1 {
			t.Fatalf("stamped %q: %d model calls", stamped, len(llm.calls))
		}
		got := ""
		if llm.calls[0].Config != nil && llm.calls[0].Config.Labels != nil {
			got = llm.calls[0].Config.Labels[modelgatewayclient.LabelActionCode]
		}
		if got != want {
			t.Errorf("stamped %q: gateway action code label = %q, want %q", stamped, got, want)
		}
	}
}

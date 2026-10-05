package tool

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"google.golang.org/grpc"

	consumptionv1 "github.com/apollo-chora/chora-contracts/gen/go/chora/services/consumption/v1"
)

type fakeWeaknessReader struct {
	edges   []WeaknessEdgeView
	err     error
	gotArgs []any
}

func (f *fakeWeaknessReader) ReadWeakness(_ context.Context, tenantID, familiarID, callerGCID string, limit int) ([]WeaknessEdgeView, error) {
	f.gotArgs = []any{tenantID, familiarID, callerGCID, limit}
	return f.edges, f.err
}

func TestNewWeaknessReader_stubMeansAbsent(t *testing.T) {
	for _, ep := range []string{"", "stub://chora-consumption"} {
		r, closeFn, err := NewWeaknessReader(ep)
		if err != nil || r != nil || closeFn == nil {
			t.Errorf("endpoint %q: reader=%v err=%v (a stub endpoint must yield NO reader, so the tool is not registered)", ep, r, err)
		}
	}
	r, closeFn, err := NewWeaknessReader("chora-consumption.consumption.svc.cluster.local:9090")
	if err != nil || r == nil {
		t.Fatalf("real endpoint must build a client lazily: %v", err)
	}
	_ = closeFn()
}

func TestReadWeakness_identityFromStateAndClampedLimit(t *testing.T) {
	f := &fakeWeaknessReader{edges: []WeaknessEdgeView{{ConceptLabel: "Fractions", Strength: 0.8, Status: "active"}}}
	resp, err := ReadWeakness(context.Background(), f, WeaknessReadRequest{Limit: 500}, "t1", "fam-1", "g1")
	if err != nil || len(resp.Edges) != 1 || resp.Note != "" {
		t.Fatalf("resp=%+v err=%v", resp, err)
	}
	if f.gotArgs[0] != "t1" || f.gotArgs[1] != "fam-1" || f.gotArgs[2] != "g1" || f.gotArgs[3] != maxWeaknessLimit {
		t.Errorf("args = %v", f.gotArgs)
	}
	_, _ = ReadWeakness(context.Background(), f, WeaknessReadRequest{}, "t1", "fam-1", "g1")
	if f.gotArgs[3] != defaultWeaknessLimit {
		t.Errorf("default limit = %v", f.gotArgs[3])
	}
	if _, err := ReadWeakness(context.Background(), f, WeaknessReadRequest{}, "", "fam-1", "g1"); err == nil || !strings.Contains(err.Error(), "un-scoped") {
		t.Errorf("missing tenant must be refused: %v", err)
	}
	if _, err := ReadWeakness(context.Background(), nil, WeaknessReadRequest{}, "t1", "fam-1", "g1"); err == nil {
		t.Errorf("nil reader must be an error")
	}
	empty := &fakeWeaknessReader{}
	resp, _ = ReadWeakness(context.Background(), empty, WeaknessReadRequest{}, "t1", "fam-1", "g1")
	if resp.Note == "" || len(resp.Edges) != 0 {
		t.Errorf("empty read must say so: %+v", resp)
	}
	boom := &fakeWeaknessReader{err: errors.New("consumption down")}
	if _, err := ReadWeakness(context.Background(), boom, WeaknessReadRequest{}, "t1", "fam-1", "g1"); err == nil {
		t.Errorf("consumption error must surface, never a placeholder")
	}
}

// A cross-service call from this agent left NO evidence on the calling side:
// the chat pods emit two lines per turn (agent_terminated_emit,
// agentdispatch.completed), nothing about tool calls, and the pod carries no
// istio-proxy, so there are no mesh access logs either. That blindness is the
// same shape that let a missing consumer sit unnoticed while both halves were
// green. These lines make the dependency call provable from the caller.

// fakeGrowthClient embeds the generated interface so only ReadWeakness needs a
// body; any other method would panic loudly rather than silently returning zero.
type fakeGrowthClient struct {
	consumptionv1.CompanionGrowthClient
	err  error
	resp *consumptionv1.ReadWeaknessResponse
}

func (f *fakeGrowthClient) ReadWeakness(_ context.Context, _ *consumptionv1.ReadWeaknessRequest, _ ...grpc.CallOption) (*consumptionv1.ReadWeaknessResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.resp != nil {
		return f.resp, nil
	}
	return &consumptionv1.ReadWeaknessResponse{}, nil
}

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

func TestReadWeakness_logsEveryInvocationWithItsOutcome(t *testing.T) {
	ctx := context.Background()
	cases := map[string]struct {
		reader  WeaknessReader
		tenant  string
		outcome string
	}{
		"ok":       {reader: &fakeWeaknessReader{edges: []WeaknessEdgeView{{ConceptLabel: "fractions"}}}, tenant: "t1", outcome: "ok"},
		"error":    {reader: &fakeWeaknessReader{err: errors.New("consumption down")}, tenant: "t1", outcome: "error"},
		"unwired":  {reader: nil, tenant: "t1", outcome: "refused"},
		"unscoped": {reader: &fakeWeaknessReader{}, tenant: "", outcome: "refused"},
	}
	for name, tc := range cases {
		buf := captureLogs(t)
		_, _ = ReadWeakness(ctx, tc.reader, WeaknessReadRequest{}, tc.tenant, "f1", "g1")
		out := buf.String()
		if !strings.Contains(out, `"msg":"companion_chat.tool.invoked"`) {
			t.Errorf("%s: no tool-invocation line; the call leaves no evidence on the calling side: %s", name, out)
			continue
		}
		if !strings.Contains(out, `"tool":"weakness.read"`) {
			t.Errorf("%s: line does not name the tool: %s", name, out)
		}
		if !strings.Contains(out, `"outcome":"`+tc.outcome+`"`) {
			t.Errorf("%s: want outcome %q in: %s", name, tc.outcome, out)
		}
	}
}

func TestGRPCWeaknessReader_logsTheMethodItDialled(t *testing.T) {
	// The METHOD is what an overlap-cover window hides: a success proves the call
	// resolved, not WHICH contract served it. Only the real dialler knows.
	buf := captureLogs(t)
	r := &grpcWeaknessReader{client: &fakeGrowthClient{err: errors.New("unavailable")}}
	_, _ = r.ReadWeakness(context.Background(), "t1", "f1", "g1", 5)
	out := buf.String()
	if !strings.Contains(out, `"msg":"companion_chat.tool.dialled"`) {
		t.Fatalf("no dial line: %s", out)
	}
	if !strings.Contains(out, consumptionv1.CompanionGrowth_ReadWeakness_FullMethodName) {
		t.Errorf("dial line must name the full method %q: %s", consumptionv1.CompanionGrowth_ReadWeakness_FullMethodName, out)
	}
}

package tool

// weakness_read_dial_test.go — the dialler's success path and its dial refusal.
//
// The existing suite drives grpcWeaknessReader only through its ERROR leg, so
// the mapping that actually reaches the Companion (proto WeaknessEdge to the
// learner-safe WeaknessEdgeView) had never been executed. That mapping is the
// privacy fence: it is what decides which proto fields cross into the prompt.
// A fence nothing exercises is a fence nobody has watched hold.

import (
	"context"
	"strings"
	"testing"

	consumptionv1 "github.com/apollo-chora/chora-contracts/gen/go/chora/services/consumption/v1"
)

func TestGRPCWeaknessReader_mapsOnlyLearnerSafeFieldsOnSuccess(t *testing.T) {
	buf := captureLogs(t)
	r := &grpcWeaknessReader{client: &fakeGrowthClient{resp: &consumptionv1.ReadWeaknessResponse{
		Edges: []*consumptionv1.WeaknessEdge{
			{
				ConceptLabel: "momentum",
				Strength:     0.8,
				Category:     "mechanics",
				Tags:         []string{"physics", "newton"},
				Status:       "active",
				Summary:      "mixes momentum with speed",
			},
			{ConceptLabel: "friction", Strength: 0.3, Status: "grown"},
		},
	}}}

	got, err := r.ReadWeakness(context.Background(), "t1", "f1", "g1", 5)
	if err != nil {
		t.Fatalf("a healthy consumption must not error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want both edges mapped; got %d", len(got))
	}
	want := WeaknessEdgeView{
		ConceptLabel: "momentum",
		Strength:     0.8,
		Category:     "mechanics",
		Tags:         []string{"physics", "newton"},
		Status:       "active",
		Summary:      "mixes momentum with speed",
	}
	if got[0].ConceptLabel != want.ConceptLabel || got[0].Strength != want.Strength ||
		got[0].Category != want.Category || got[0].Status != want.Status || got[0].Summary != want.Summary {
		t.Errorf("edge mapped wrong:\n got %+v\nwant %+v", got[0], want)
	}
	if len(got[0].Tags) != 2 || got[0].Tags[0] != "physics" {
		t.Errorf("tags lost in mapping: %+v", got[0].Tags)
	}

	// The dial line is the CALLING side's only evidence the dependency was
	// reached; a success must leave one behind, naming the method and the count.
	out := buf.String()
	if !strings.Contains(out, `"outcome":"ok"`) || !strings.Contains(out, `"edges":2`) {
		t.Errorf("a successful dial must log outcome ok with the edge count: %s", out)
	}
	if !strings.Contains(out, consumptionv1.CompanionGrowth_ReadWeakness_FullMethodName) {
		t.Errorf("the dial line must name the full method: %s", out)
	}
}

func TestGRPCWeaknessReader_returnsAnEmptySliceWhenThereAreNoEdges(t *testing.T) {
	r := &grpcWeaknessReader{client: &fakeGrowthClient{}}
	got, err := r.ReadWeakness(context.Background(), "t1", "f1", "g1", 5)
	if err != nil {
		t.Fatalf("no edges is not an error: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("want an empty slice, not nil: %#v", got)
	}
}

func TestNewWeaknessReader_refusesAnUnparseableEndpoint(t *testing.T) {
	// What this guard actually catches, measured rather than assumed:
	// grpc.NewClient is LAZY about reachability, so a wrong-but-parseable target
	// ("no-such-scheme://consumption", "dns:///host:not-a-port", even "::::")
	// builds a client happily and only fails at call time, through ReadWeakness's
	// error leg. The one thing NewClient rejects up front is a target it cannot
	// parse as a URL, which is the shape a mangled env var arrives in. That is
	// the branch under test, and it is the only one this constructor can refuse.
	r, closeFn, err := NewWeaknessReader("dns:///%zz:9090")
	if err == nil {
		t.Fatalf("an unparseable endpoint must be refused; got reader=%v closer-present=%v", r, closeFn != nil)
	}
	if !strings.Contains(err.Error(), "weakness.read: dial consumption") {
		t.Errorf("refusal must name the tool and the endpoint; got %v", err)
	}
	if r != nil || closeFn != nil {
		t.Errorf("a refused dial must return no reader and no closer; got reader=%v closer-present=%v", r, closeFn != nil)
	}
}

func TestNewWeaknessReader_stubCloserIsANoOp(t *testing.T) {
	// The stub branch hands back a closer so callers can defer unconditionally;
	// calling it must be safe rather than a nil-func panic at shutdown.
	_, closeFn, err := NewWeaknessReader("stub://chora-consumption")
	if err != nil {
		t.Fatalf("stub endpoint must not error: %v", err)
	}
	if err := closeFn(); err != nil {
		t.Errorf("the stub closer must be a safe no-op; got %v", err)
	}
}

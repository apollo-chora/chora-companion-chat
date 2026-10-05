package tool

// weakness_read_clamp_test.go — the int32 narrowing at the transport boundary.
//
// grpcWeaknessReader.ReadWeakness takes an int and puts it on a wire field
// that is int32. The tool handler clamps to [1, maxWeaknessLimit] before it
// calls this, but that is an invariant held two frames up: it is not visible
// to the compiler, not visible to gosec (which reported it as G115/CWE-190),
// and not inherited by any other caller of the WeaknessReader interface.
// These tests pin the bound at the place the conversion actually happens.

import (
	"context"
	"testing"

	consumptionv1 "github.com/apollo-chora/chora-contracts/gen/go/chora/services/consumption/v1"
	"google.golang.org/grpc"
)

// capturingGrowthClient records the request that reached the wire. The
// sibling fakeGrowthClient deliberately discards it, and what is under test
// here IS the request field, so it needs its own double rather than a change
// to one another test already depends on.
type capturingGrowthClient struct {
	consumptionv1.CompanionGrowthClient
	got *consumptionv1.ReadWeaknessRequest
}

func (c *capturingGrowthClient) ReadWeakness(_ context.Context, in *consumptionv1.ReadWeaknessRequest, _ ...grpc.CallOption) (*consumptionv1.ReadWeaknessResponse, error) {
	c.got = in
	return &consumptionv1.ReadWeaknessResponse{}, nil
}

func TestGRPCWeaknessReader_clampsTheLimitAtTheInt32Narrowing(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   int
		want int32
	}{
		{"in range passes through", 25, 25},
		{"above the cap is clamped to the cap", 10_000, maxWeaknessLimit},
		{"a value that would overflow int32 is clamped, not wrapped", 1 << 40, maxWeaknessLimit},
		{"negative is floored at zero rather than sent negative", -7, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &capturingGrowthClient{}
			r := &grpcWeaknessReader{client: c}
			if _, err := r.ReadWeakness(context.Background(), "t1", "f1", "g1", tc.in); err != nil {
				t.Fatalf("ReadWeakness returned %v", err)
			}
			if c.got == nil {
				t.Fatal("no request reached the wire, so the assertion below would be vacuous")
			}
			if c.got.GetLimit() != tc.want {
				t.Fatalf("wire limit = %d, want %d (called with %d)", c.got.GetLimit(), tc.want, tc.in)
			}
		})
	}
}

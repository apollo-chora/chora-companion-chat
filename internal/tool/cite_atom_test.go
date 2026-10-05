package tool

import (
	"context"
	"net"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	creationv1 "github.com/apollo-chora/chora-contracts/gen/go/chora/services/creation/v1"
)

const testTenantID = "01957c8c-0000-7000-8888-000088880000"

// ---------- StubValidator semantics (sandbox corpus) ----------

func TestCiteAtom_stubModeAcceptsStubAtomIDs(t *testing.T) {
	resp, err := CiteAtom(context.Background(), StubValidator{}, CiteAtomRequest{
		AtomID: "atom-stub-001",
	}, testTenantID)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !resp.Exists {
		t.Errorf("stub mode must accept atom-stub-* ids; got Exists=false")
	}
	if resp.CurrentRevisionID == "" {
		t.Errorf("stub mode must surface a synthetic revision id; got empty")
	}
}

func TestCiteAtom_stubModeRejectsNonStubAtomIDs(t *testing.T) {
	resp, err := CiteAtom(context.Background(), StubValidator{}, CiteAtomRequest{
		AtomID: "atom-fabricated-by-llm-deadbeef",
	}, testTenantID)
	if err != nil {
		t.Fatalf("stub mode must NOT error on a fabricated id (the verdict goes through Exists); got err=%v", err)
	}
	if resp.Exists {
		t.Errorf("stub mode must reject non-stub atom_id (anti-fabrication guard); got Exists=true")
	}
	if resp.Reason == "" {
		t.Errorf("rejected verdict must carry a Reason for the audit trail")
	}
}

// ---------- Input guards ----------

func TestCiteAtom_rejectsEmptyAtomID(t *testing.T) {
	if _, err := CiteAtom(context.Background(), StubValidator{}, CiteAtomRequest{AtomID: ""}, testTenantID); err == nil {
		t.Fatal("want error for empty atom_id")
	}
}

func TestCiteAtom_rejectsEmptyTenantID(t *testing.T) {
	if _, err := CiteAtom(context.Background(), StubValidator{}, CiteAtomRequest{AtomID: "atom-stub-001"}, ""); err == nil {
		t.Fatal("want error for empty tenant_id (RLS guard; handler sources it from session state)")
	}
}

func TestCiteAtom_rejectsNilValidator(t *testing.T) {
	if _, err := CiteAtom(context.Background(), nil, CiteAtomRequest{AtomID: "atom-stub-001"}, testTenantID); err == nil {
		t.Fatal("want error for nil validator (boot wiring bug must fail loud)")
	}
}

// ---------- NewValidator selection ----------

func TestNewValidator_refusesEmptyEndpoint(t *testing.T) {
	_, _, err := NewValidator("")
	if err == nil {
		t.Fatal("want error for missing CREATION_GRPC_ENDPOINT (feedback_no_inline_config)")
	}
	if !strings.Contains(err.Error(), "CREATION_GRPC_ENDPOINT") {
		t.Errorf("error should reference the env var name; got %v", err)
	}
}

func TestNewValidator_selectsStubForStubScheme(t *testing.T) {
	v, closer, err := NewValidator("stub://chora-creation")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	defer func() { _ = closer() }()
	if _, ok := v.(StubValidator); !ok {
		t.Fatalf("stub:// endpoint must select StubValidator; got %T", v)
	}
}

func TestNewValidator_selectsGRPCForRealEndpoint(t *testing.T) {
	v, closer, err := NewValidator("chora-creation.creation.svc.cluster.local:9090")
	if err != nil {
		t.Fatalf("real endpoint must construct a gRPC validator without dialling eagerly; got %v", err)
	}
	defer func() { _ = closer() }()
	if _, ok := v.(*grpcValidator); !ok {
		t.Fatalf("real endpoint must select the gRPC validator; got %T", v)
	}
}

// ---------- Real client against a fake chora-creation server ----------

// fakeCreationServer implements ValidateAtomID with a one-atom corpus, so the
// test drives the live endpoint shape (ADR-249 A1a RED requirement: a genuine
// atom id must be confirmable, not rejected as fabricated).
type fakeCreationServer struct {
	creationv1.UnimplementedCreationServer
	knownAtomID  string
	knownTenant  string
	lastTenantID string
}

func (f *fakeCreationServer) ValidateAtomID(_ context.Context, in *creationv1.ValidateAtomIDRequest) (*creationv1.ValidateAtomIDResponse, error) {
	f.lastTenantID = in.GetTenantId()
	if in.GetAtomId() == f.knownAtomID && in.GetTenantId() == f.knownTenant {
		return &creationv1.ValidateAtomIDResponse{Exists: true, CurrentRevisionId: "rev-real-1"}, nil
	}
	return &creationv1.ValidateAtomIDResponse{Exists: false}, nil
}

func newBufconnValidator(t *testing.T, srv *fakeCreationServer) Validator {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	grpcSrv := grpc.NewServer()
	creationv1.RegisterCreationServer(grpcSrv, srv)
	go func() { _ = grpcSrv.Serve(lis) }()
	t.Cleanup(grpcSrv.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("bufconn client: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return &grpcValidator{client: creationv1.NewCreationClient(conn)}
}

func TestCiteAtom_realClientConfirmsGenuineAtom(t *testing.T) {
	genuine := "01957c8c-aaaa-7000-bbbb-cccccccccccc"
	srv := &fakeCreationServer{knownAtomID: genuine, knownTenant: testTenantID}
	v := newBufconnValidator(t, srv)

	resp, err := CiteAtom(context.Background(), v, CiteAtomRequest{AtomID: genuine}, testTenantID)
	if err != nil {
		t.Fatalf("genuine atom must validate without error; got %v", err)
	}
	if !resp.Exists {
		t.Fatalf("genuine atom must NOT be rejected as fabricated (the inverted-guard defect); got Exists=false reason=%q", resp.Reason)
	}
	if resp.CurrentRevisionID != "rev-real-1" {
		t.Errorf("want the server's revision pointer; got %q", resp.CurrentRevisionID)
	}
	if srv.lastTenantID != testTenantID {
		t.Errorf("tenant scope must reach the server; got %q", srv.lastTenantID)
	}
}

func TestCiteAtom_realClientRejectsUnknownAtomWithoutError(t *testing.T) {
	srv := &fakeCreationServer{knownAtomID: "01957c8c-aaaa-7000-bbbb-cccccccccccc", knownTenant: testTenantID}
	v := newBufconnValidator(t, srv)

	resp, err := CiteAtom(context.Background(), v, CiteAtomRequest{AtomID: "01957c8c-ffff-7000-ffff-ffffffffffff"}, testTenantID)
	if err != nil {
		t.Fatalf("unknown atom is a verdict, not an error; got %v", err)
	}
	if resp.Exists {
		t.Fatal("unknown atom must not validate")
	}
	if resp.Reason == "" {
		t.Error("rejected verdict must carry a Reason for the audit trail")
	}
}

func TestCiteAtom_realClientSurfacesTransportErrorLoudly(t *testing.T) {
	// A dead endpoint must error, never silently read as "fabricated".
	conn, err := grpc.NewClient("passthrough:///127.0.0.1:9",
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("constructing client: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	v := &grpcValidator{client: creationv1.NewCreationClient(conn)}

	_, err = CiteAtom(context.Background(), v, CiteAtomRequest{AtomID: "01957c8c-aaaa-7000-bbbb-cccccccccccc"}, testTenantID)
	if err == nil {
		t.Fatal("transport failure must surface as an error (fail loud), not a fabricated verdict")
	}
}

package tool

// weakness_read.go: the weakness.read tool (ADR-254 D6 / R3: diagnosis READ is a
// tool, the request is an event, the voice is a dispatched turn). It reads the
// learner's current Growth Edges from chora-consumption's FamiliarGrowthService
// ReadWeakness, which resolves and ownership-checks the Companion server-side
// and returns a learner-SAFE projection (labels, strength order, summaries;
// never ids). Tenant and caller identity are taken from session state, never
// from the model.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	consumptionv1 "github.com/apollo-chora/chora-contracts/gen/go/chora/services/consumption/v1"
)

// WeaknessReadRequest is the tool's input. The model may ask for a cap only;
// identity comes from state.
type WeaknessReadRequest struct {
	Limit int `json:"limit,omitempty"`
}

// WeaknessEdgeView is one learner-safe Growth Edge as the model sees it.
type WeaknessEdgeView struct {
	ConceptLabel string   `json:"concept_label"`
	Strength     float64  `json:"strength"`
	Category     string   `json:"category,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	Status       string   `json:"status"`
	Summary      string   `json:"summary,omitempty"`
}

// WeaknessReadResponse is the tool's output.
type WeaknessReadResponse struct {
	Edges []WeaknessEdgeView `json:"edges"`
	Note  string             `json:"note,omitempty"`
}

// WeaknessReader is the narrow consumption seam; a fake stands in for tests.
type WeaknessReader interface {
	ReadWeakness(ctx context.Context, tenantID, familiarID, callerGCID string, limit int) ([]WeaknessEdgeView, error)
}

const (
	defaultWeaknessLimit = 10
	maxWeaknessLimit     = 50
	weaknessReadTimeout  = 5 * time.Second
)

// NewWeaknessReader dials consumption's FamiliarGrowthService. A stub://
// endpoint returns a nil reader: the tool is then NOT registered, because a
// tool that cannot work must be absent from the tool set rather than answer
// with a placeholder (ADR-254 D5).
func NewWeaknessReader(endpoint string) (WeaknessReader, func() error, error) {
	ep := strings.TrimSpace(endpoint)
	if ep == "" || strings.HasPrefix(ep, "stub://") {
		return nil, func() error { return nil }, nil
	}
	conn, err := grpc.NewClient(ep, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, fmt.Errorf("weakness.read: dial consumption %s: %w", ep, err)
	}
	return &grpcWeaknessReader{client: consumptionv1.NewCompanionGrowthClient(conn)}, conn.Close, nil
}

type grpcWeaknessReader struct {
	client consumptionv1.CompanionGrowthClient
}

func (g *grpcWeaknessReader) ReadWeakness(ctx context.Context, tenantID, familiarID, callerGCID string, limit int) ([]WeaknessEdgeView, error) {
	ctx, cancel := context.WithTimeout(ctx, weaknessReadTimeout)
	defer cancel()
	// The wire field is int32 and the narrowing happens HERE, while the only
	// thing bounding it is a clamp in the tool handler two frames up. That is
	// an invariant a future caller of this interface method does not inherit
	// and neither the compiler nor gosec can see, so it is re-established at
	// the conversion rather than assumed (gosec G115, CWE-190).
	lim := limit
	if lim < 0 {
		lim = 0
	}
	if lim > maxWeaknessLimit {
		lim = maxWeaknessLimit
	}
	resp, err := g.client.ReadWeakness(ctx, &consumptionv1.ReadWeaknessRequest{
		TenantId: tenantID, CompanionId: familiarID, CallerGcid: callerGCID, Limit: int32(lim),
	})
	// The METHOD is what an overlap-cover window hides: a success proves the
	// call resolved, not WHICH contract served it, and only the dialler knows.
	if err != nil {
		slog.WarnContext(ctx, "companion_chat.tool.dialled",
			"tool", ToolNameWeaknessRead,
			"method", consumptionv1.CompanionGrowth_ReadWeakness_FullMethodName,
			"outcome", "error", "error", err.Error())
		return nil, fmt.Errorf("weakness.read: consumption ReadWeakness: %w", err)
	}
	slog.InfoContext(ctx, "companion_chat.tool.dialled",
		"tool", ToolNameWeaknessRead,
		"method", consumptionv1.CompanionGrowth_ReadWeakness_FullMethodName,
		"outcome", "ok", "edges", len(resp.GetEdges()))
	out := make([]WeaknessEdgeView, 0, len(resp.GetEdges()))
	for _, e := range resp.GetEdges() {
		out = append(out, WeaknessEdgeView{
			ConceptLabel: e.GetConceptLabel(), Strength: e.GetStrength(), Category: e.GetCategory(),
			Tags: e.GetTags(), Status: e.GetStatus(), Summary: e.GetSummary(),
		})
	}
	return out, nil
}

// ReadWeakness is the tool handler body: identity from state, cap clamped,
// edges returned learner-safe. An absent identity is refused (never an
// un-scoped read).
func ReadWeakness(ctx context.Context, r WeaknessReader, req WeaknessReadRequest, tenantID, familiarID, callerGCID string) (resp WeaknessReadResponse, err error) {
	// ONE line per invocation, whichever way this returns. Without it a
	// cross-service call from this agent leaves no evidence on the CALLING
	// side at all: the pod emits two lines per turn and carries no
	// istio-proxy, so nothing here could show a dependency was reached. That
	// blindness is the shape that lets a missing wire sit unnoticed while both
	// halves look green.
	outcome := "ok"
	defer func() {
		slog.InfoContext(ctx, "companion_chat.tool.invoked",
			"tool", ToolNameWeaknessRead,
			"outcome", outcome,
			"edges", len(resp.Edges),
			"error", errText(err))
	}()
	if r == nil {
		outcome = "refused"
		return WeaknessReadResponse{}, errors.New("weakness.read: reader not wired")
	}
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(familiarID) == "" || strings.TrimSpace(callerGCID) == "" {
		outcome = "refused"
		return WeaknessReadResponse{}, errors.New("weakness.read: tenant_id, familiar_id and user_gcid must be in session state; refusing an un-scoped read")
	}
	limit := req.Limit
	if limit <= 0 {
		limit = defaultWeaknessLimit
	}
	if limit > maxWeaknessLimit {
		limit = maxWeaknessLimit
	}
	edges, err := r.ReadWeakness(ctx, tenantID, familiarID, callerGCID, limit)
	if err != nil {
		outcome = "error"
		return WeaknessReadResponse{}, err
	}
	resp = WeaknessReadResponse{Edges: edges}
	if len(edges) == 0 {
		resp.Note = "no Growth Edges on record for this learner yet"
	}
	return resp, nil
}

// ToolNameWeaknessRead is the tool's stable log identity (matches boot's tool key).
const ToolNameWeaknessRead = "weakness.read"

// errText keeps a nil error out of the log as an empty field rather than "<nil>".
func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

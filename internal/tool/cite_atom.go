// Package tool holds the stateless tool functions called by the Familiar
// ADK agent. Each is a pure adapter over an upstream Chora gRPC service.
// NO domain logic lives here, only the adapter wiring.
//
// ADR-249 A1a (owner-ruled 2026-08-07): cite_atom is the agent's ONLY tool.
// The three consumption-backed tools (atom_search / persona_lookup /
// ebbinghaus_state ladder names) were removed with their never-implemented
// clients; mid-turn retrieval returns as the ADR-249 Option B
// consumption-mediated callback, decided once with requirement S0.
//
// Per `adk-tool-calling-loop` SKILL conventions: tools emit OTLP spans
// with OpenInference semantic conventions.
package tool

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	creationv1 "github.com/apollo-chora/chora-contracts/gen/go/chora/services/creation/v1"
)

// CiteAtomRequest is the agent-provided input. The model supplies ONLY the
// atom_id; tenant scope is injected by the handler from session state
// (stamped by chora-consumption at CreateSession), never trusted from the
// model, so a prompt-injected tenant_id cannot re-scope the lookup.
type CiteAtomRequest struct {
	// AtomID is the atom_id the LLM emitted that the agent wants to cite.
	// Required, non-empty.
	AtomID string `json:"atom_id"`
}

// CiteAtomResponse is the tool's output.
type CiteAtomResponse struct {
	// AtomID echoes the id that was validated. The agent harvests the
	// citation off this response, so the verdict has to name its own
	// subject; without it a confirmed atom cannot be attributed to
	// anything and the grounding list is structurally always empty.
	// Populated for BOTH verdicts so a refusal stays attributable.
	AtomID string `json:"atom_id"`

	// Exists is true iff the atom_id resolves to a non-soft-deleted atom in
	// the tenant. False = "fabricated / unknown / soft-deleted" (the three
	// states are intentionally collapsed at the server side to prevent
	// cross-tenant probing oracles).
	Exists bool `json:"exists"`

	// CurrentRevisionID is the atom's current published revision_id when
	// Exists is true AND the atom has at least one published revision.
	// Empty otherwise.
	CurrentRevisionID string `json:"current_revision_id,omitempty"`

	// Reason captures the verdict for the audit trail (always populated on
	// Exists=false; optional on Exists=true).
	Reason string `json:"reason,omitempty"`
}

// Validator answers "may this atom_id be cited for this tenant".
type Validator interface {
	ValidateAtomID(ctx context.Context, atomID, tenantID string) (CiteAtomResponse, error)
}

// NewValidator selects the Validator implementation from the endpoint:
//
//   - "stub://…"  → StubValidator (deterministic atom-stub-* corpus; sandbox)
//   - anything else → gRPC client to chora-creation.ValidateAtomID
//     (creation_server.go, PG-backed, RLS-tenant-scoped)
//
// The gRPC dial uses plaintext (insecure) transport: the familiar pod is
// sidecar-less per ADR-169 and the domain service's mesh sidecar admits
// cleartext on its PERMISSIVE gRPC port, mirroring the CONSUMPTION_GRPC_ENDPOINT
// posture in cmd/familiar/main.go.
//
// An empty endpoint refuses per feedback_no_inline_config. The returned
// closer releases the gRPC connection (no-op for the stub).
func NewValidator(endpoint string) (Validator, func() error, error) {
	ep := strings.TrimSpace(endpoint)
	if ep == "" {
		return nil, nil, errors.New(
			"CREATION_GRPC_ENDPOINT not set; refusing inline default " +
				"per feedback_no_inline_config")
	}
	if strings.HasPrefix(ep, "stub://") {
		return StubValidator{}, func() error { return nil }, nil
	}
	conn, err := grpc.NewClient(ep, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, fmt.Errorf("cite_atom: dial chora-creation %q: %w", ep, err)
	}
	return &grpcValidator{client: creationv1.NewCreationClient(conn)}, conn.Close, nil
}

// StubValidator accepts only atom-stub-* identifiers, mirroring the retired
// atom_search stub corpus so sandbox smoke runs stay coherent.
type StubValidator struct{}

// ValidateAtomID satisfies Validator with the deterministic stub verdict.
func (StubValidator) ValidateAtomID(_ context.Context, atomID, _ string) (CiteAtomResponse, error) {
	if strings.HasPrefix(atomID, "atom-stub-") {
		return CiteAtomResponse{
			Exists:            true,
			CurrentRevisionID: fmt.Sprintf("rev-stub-%s-1", atomID),
		}, nil
	}
	return CiteAtomResponse{
		Exists: false,
		Reason: "atom_id not found in stub corpus: likely fabricated",
	}, nil
}

// grpcValidator calls chora-creation.ValidateAtomID (the real corpus).
type grpcValidator struct {
	client creationv1.CreationClient
}

// validateRPCTimeout bounds a single citation check; the turn must not hang
// on a slow domain call (the agent's write timeout is 120s, a citation is a
// primary-key existence probe).
const validateRPCTimeout = 5 * time.Second

func (g *grpcValidator) ValidateAtomID(ctx context.Context, atomID, tenantID string) (CiteAtomResponse, error) {
	cctx, cancel := context.WithTimeout(ctx, validateRPCTimeout)
	defer cancel()
	resp, err := g.client.ValidateAtomID(cctx, &creationv1.ValidateAtomIDRequest{
		AtomId:   atomID,
		TenantId: tenantID,
	})
	if err != nil {
		// Fail loud: an unreachable corpus is an error, never a silent
		// Exists=false (which would read as "fabricated" to the model).
		return CiteAtomResponse{}, fmt.Errorf("cite_atom: chora-creation ValidateAtomID: %w", err)
	}
	out := CiteAtomResponse{
		Exists:            resp.GetExists(),
		CurrentRevisionID: resp.GetCurrentRevisionId(),
	}
	if !out.Exists {
		out.Reason = "atom_id not found for tenant (unknown, soft-deleted, or fabricated)"
	}
	return out, nil
}

// CiteAtom validates an atom_id before the LLM may emit it as a citation.
// tenantID comes from session state via the handler (RLS scope at the
// server side); v is constructed once at boot by NewValidator.
func CiteAtom(ctx context.Context, v Validator, req CiteAtomRequest, tenantID string) (CiteAtomResponse, error) {
	tracer := otel.Tracer("familiar_adk_go/tool/cite_atom")
	ctx, span := tracer.Start(ctx, "tool.cite_atom")
	defer span.End()

	span.SetAttributes(
		attribute.String("openinference.span.kind", "TOOL"),
		attribute.String("tool.name", "cite_atom"),
	)

	if v == nil {
		return CiteAtomResponse{}, errors.New("cite_atom: validator not wired (programmer error; main.go constructs it at boot)")
	}
	if strings.TrimSpace(req.AtomID) == "" {
		return CiteAtomResponse{}, errors.New("cite_atom: atom_id is required")
	}
	if strings.TrimSpace(tenantID) == "" {
		return CiteAtomResponse{}, errors.New("cite_atom: tenant_id is required (RLS guard; sourced from session state)")
	}
	span.SetAttributes(
		attribute.String("tool.input.atom_id", req.AtomID),
		attribute.String("tool.input.tenant_id", tenantID),
	)

	resp, err := v.ValidateAtomID(ctx, req.AtomID, tenantID)
	if err != nil {
		span.SetAttributes(attribute.String("tool.output.error", err.Error()))
		return CiteAtomResponse{}, err
	}
	// Stamped here, not in each Validator, so no implementation can omit it.
	resp.AtomID = req.AtomID
	span.SetAttributes(
		attribute.Bool("tool.output.exists", resp.Exists),
		attribute.String("tool.output.current_revision_id", resp.CurrentRevisionID),
	)
	if !resp.Exists && resp.Reason != "" {
		span.SetAttributes(attribute.String("tool.output.reason", resp.Reason))
	}
	return resp, nil
}

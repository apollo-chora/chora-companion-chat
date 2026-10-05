package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	adkagent "google.golang.org/adk/agent"

	"github.com/apollo-chora/chora-companion-chat/internal/skillregistry"
)

// AppNamePrefix is the convention used by callers in the per-session deploy
// variant to bind a session to a specific Familiar instance — the
// caller sets session.AppName = "familiar:{familiar_id}" and the adkrest
// controller calls `c.agentLoader.LoadAgent(req.AppName)` per
// `google.golang.org/adk/server/adkrest/controllers/runtime.go:201`.
//
// ⚠️ The ADK SDK's streaming-query controller does NOT use this prefix: it
// hardcodes AppName to the numeric engine id and bypasses LoadAgent in favour
// of RootAgent (per `google.golang.org/adk/server/agentengine/controllers/method/
// stream_query.go:192,195`). Multi-Familiar dispatch flows through
// `chora-adk-common/instancedispatch` reading session.State() familiar_id
// instead — see ADR-147 §7 + dispatch.go (NewFamiliarResolver).
//
// PerSessionLoader remains valid for the per-session variant + direct REST
// clients hitting an agent's local web port.
const AppNamePrefix = "familiar:"

// ErrMalformedAppName is returned by LoadAgent when the requested name
// doesn't follow the "familiar:{familiar_id}" convention.
var ErrMalformedAppName = errors.New("malformed app_name")

// AgentFactory builds an adk Agent for a specific Familiar's config.
// Production: closure over the gemini model + chora_consumption gRPC clients
// + chora-content-agents-go tools. Tests: closure capturing the cfg for
// inspection.
type AgentFactory func(ctx context.Context, cfg *skillregistry.FamiliarConfig) (adkagent.Agent, error)

// PerSessionLoader implements agent.Loader by parsing the requested
// app_name as "familiar:{familiar_id}", looking up the per-Familiar config in
// the skill registry, and dispatching to the AgentFactory.
//
// Pattern P1 single-agent ReAct (per ADR-147 §1) + session-time per-instance
// configuration (per ADR-147 §7).
type PerSessionLoader struct {
	factory   AgentFactory
	registry  skillregistry.Registry
	rootAgent adkagent.Agent // pre-built specimen returned by RootAgent() for warm-up
}

// NewPerSessionLoader wires the dependencies. rootAgent is the specimen
// returned by RootAgent() (the ADK SDK's streaming-query controller uses it
// for warm-up + when the requested app_name is empty). Production: a default
// math Familiar specimen built at process start.
func NewPerSessionLoader(
	factory AgentFactory,
	registry skillregistry.Registry,
	rootAgent adkagent.Agent,
) (*PerSessionLoader, error) {
	if factory == nil {
		return nil, fmt.Errorf("PerSessionLoader: factory is required")
	}
	if registry == nil {
		return nil, fmt.Errorf("PerSessionLoader: registry is required")
	}
	if rootAgent == nil {
		return nil, fmt.Errorf("PerSessionLoader: rootAgent (warm-up specimen) is required")
	}
	return &PerSessionLoader{
		factory:   factory,
		registry:  registry,
		rootAgent: rootAgent,
	}, nil
}

// ListAgents reports the single logical agent name `familiar_companion`.
// Specific Familiar instances are addressed via the app_name convention
// (parsed by LoadAgent); they are NOT enumerated here (there are potentially
// millions across tenants).
func (l *PerSessionLoader) ListAgents() []string {
	return []string{Name}
}

// LoadAgent resolves a requested app_name to a per-session Familiar agent.
//
// Accepted shapes:
//   - "" or Name ("familiar_companion") → rootAgent (warm-up specimen)
//   - "familiar:{familiar_id}"          → per-Familiar agent built by factory
//   - anything else                     → ErrMalformedAppName
func (l *PerSessionLoader) LoadAgent(name string) (adkagent.Agent, error) {
	switch name {
	case "", Name:
		return l.rootAgent, nil
	}
	familiarID, err := ParseFamiliarIDFromAppName(name)
	if err != nil {
		return nil, err
	}
	cfg, err := l.registry.LoadFamiliarConfig(context.Background(), familiarID)
	if err != nil {
		return nil, fmt.Errorf("load familiar %s: %w", familiarID, err)
	}
	a, err := l.factory(context.Background(), cfg)
	if err != nil {
		return nil, fmt.Errorf("build familiar %s: %w", familiarID, err)
	}
	if a == nil {
		return nil, fmt.Errorf("factory returned nil agent for familiar %s", familiarID)
	}
	return a, nil
}

// RootAgent returns the warm-up specimen passed at construction.
// The ADK SDK's streaming-query controller uses this for queries that don't
// carry an explicit app_name (per adk@v1.2.1 server/agentengine/.../stream_query.go).
func (l *PerSessionLoader) RootAgent() adkagent.Agent {
	return l.rootAgent
}

// ParseFamiliarIDFromAppName extracts the familiar_id from a session AppName
// of the form "familiar:{familiar_id}". Returns ErrMalformedAppName otherwise.
// Pure function — trivially testable.
func ParseFamiliarIDFromAppName(appName string) (string, error) {
	if !strings.HasPrefix(appName, AppNamePrefix) {
		return "", fmt.Errorf("%w: expected prefix %q, got %q", ErrMalformedAppName, AppNamePrefix, appName)
	}
	id := strings.TrimPrefix(appName, AppNamePrefix)
	if id == "" {
		return "", fmt.Errorf("%w: familiar_id is empty in %q", ErrMalformedAppName, appName)
	}
	return id, nil
}

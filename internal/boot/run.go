package boot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	adkmodel "google.golang.org/adk/model"
	"google.golang.org/adk/plugin"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"

	"github.com/apollo-chora/chora-adk-common/agentdispatch"
	"github.com/apollo-chora/chora-adk-common/modelgatewayclient"
	"github.com/apollo-chora/chora-adk-common/tracing"

	"github.com/apollo-chora/chora-companion-chat/internal/skillregistry"
	tools "github.com/apollo-chora/chora-companion-chat/internal/tool"
)

// Process-level collaborators as package variables so a unit test can prove
// the composition without a secrets resolver, Postgres or a broker.
var (
	initTracing      = tracing.Init
	newGatewayLLM    = NewGatewayLLM
	newSecretsClient = newSecretReader
	openSessionStore = func(ctx context.Context, dsn, schema string) (session.Service, error) {
		return OpenSessionStore(ctx, dsn, schema)
	}
	serveSubscriber = agentdispatch.RunSubscriberOnly
)

// Run boots and serves companion_chat subscriber-only (ADR-254 D6). A
// non-nil error means the pod must die.
func Run(ctx context.Context, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("%s takes no arguments, got %q: the ADK web launcher "+
			"(\"web -port ...\") was removed by ADR-254 D6 and this binary "+
			"is subscriber-only; update the Deployment command", CrewKind, args)
	}
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	return run(ctx, cfg)
}

func run(ctx context.Context, cfg Config) error {
	traceShutdown, err := initTracing(ctx, CrewKind)
	if err != nil {
		return fmt.Errorf("tracing.Init: %w", err)
	}
	defer func() {
		if err := traceShutdown(context.Background()); err != nil {
			slog.Error("trace shutdown error", "err", err)
		}
	}()
	slog.Info(CrewKind+" boot", cfg.LogAttrs()...)

	// Postgres session store FIRST: without it there is no conversation, so
	// nothing else is worth building.
	secretsReader, closeSecrets, err := newSecretsClient(ctx, cfg.ProjectID)
	if err != nil {
		return fmt.Errorf("secrets.NewClient: %w", err)
	}
	dsn, err := ResolveSessionsDSN(ctx, secretsReader, cfg.SessionsDSNSecretID, cfg.SessionsSchema)
	_ = closeSecrets()
	if err != nil {
		return err
	}
	// The store asserts the migrated shape (tenant_id + RLS forced + policy on
	// the four tables) and proves the GUC round-trips before anything serves.
	sessions, err := openSessionStoreWithDialRetry(ctx, dsn, cfg.SessionsSchema)
	if err != nil {
		return err
	}

	llm, err := newGatewayLLM(ctx, cfg)
	if err != nil {
		return err
	}
	citeValidator, _, err := tools.NewValidator(cfg.CreationEP)
	if err != nil {
		return fmt.Errorf("cite_atom validator: %w", err)
	}
	weaknessReader, _, err := tools.NewWeaknessReader(cfg.ConsumptionEP)
	if err != nil {
		return err
	}
	if weaknessReader == nil {
		slog.Warn("companion_chat: weakness.read tool ABSENT (CONSUMPTION_GRPC_ENDPOINT is a stub)")
	}
	var registry skillregistry.Registry
	if strings.HasPrefix(cfg.ConsumptionEP, "stub://") {
		registry = skillregistry.NewStubRegistry()
		slog.Warn("companion_chat: skill registry = STUB (3 canned Companions resolve)")
	} else {
		registry = skillregistry.NewStateRegistry()
	}
	tree, err := BuildTree(cfg, Deps{LLM: llm, Registry: registry, CiteValidator: citeValidator, WeaknessReader: weaknessReader})
	if err != nil {
		return err
	}
	plugins, err := NewPlugins(tree.DispatchCfg)
	if err != nil {
		return err
	}
	return serveSubscriber(ctx, serveConfig(tree, sessions, plugins), agentdispatch.RunOptions{})
}

// serveConfig is the lane identity plus the conversational session policy.
func serveConfig(tree Tree, sessions session.Service, plugins []*plugin.Plugin) agentdispatch.ServeConfig {
	return agentdispatch.ServeConfig{
		AgentRole:   DispatchRole,
		ServiceName: ServiceName,
		AppName:     SessionAppName,
		RootAgent:   tree.Root,
		Sessions:    sessions,
		Plugins:     runner.PluginConfig{Plugins: plugins},
		SessionKey:  SessionKey,
		UserMessage: UserMessage,
	}
}

// gatewayConfig: agent id companion_chat, surface companion_chat (ADR-254 D7);
// the per-tier action code rides per request from session state.
func gatewayConfig(cfg Config) modelgatewayclient.Config {
	return modelgatewayclient.Config{
		Endpoint:       cfg.GatewayEndpoint,
		LogicalModelID: cfg.Model,
		AgentID:        CrewKind,
		CrewKind:       CrewSurface,
		Surface:        CrewSurface,
		TenantID:       cfg.GatewayTenantID,
		GCID:           cfg.GatewayGCID,
		Audience:       cfg.GatewayAudience,
	}
}

// NewGatewayLLM builds the chora-model-gateway-fronted LLM (ADR-163).
func NewGatewayLLM(ctx context.Context, cfg Config) (adkmodel.LLM, error) {
	m, err := modelgatewayclient.New(ctx, gatewayConfig(cfg))
	if err != nil {
		return nil, fmt.Errorf("modelgatewayclient.New(%s, %s @ %s): %w", CrewKind, cfg.Model, cfg.GatewayEndpoint, err)
	}
	return m, nil
}

// sessionsDialRetryWindow bounds how long the boot waits for the session
// store to accept a connection. A connection sidecar starts alongside this
// container and may not listen yet when the first shape check dials
// (seen live 2026-08-22: "dial tcp 127.0.0.1:5432: connect: connection
// refused", exit 1, the restart booted clean). Only a DIAL fault is retried;
// a shape or GUC fault (a migration not applied, a policy missing) is
// permanent and still kills the pod at once.
const (
	sessionsDialRetryWindow = 60 * time.Second
	sessionsDialRetryPause  = time.Second
)

// isDialFault reports whether err is the store not accepting a connection
// (a net-level fault, or pgx's "failed to connect" wrapping one).
func isDialFault(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "connection refused") || strings.Contains(msg, "failed to connect to")
}

// openSessionStoreWithDialRetry opens the store, retrying a dial fault inside
// sessionsDialRetryWindow and surfacing the last error when the window closes.
func openSessionStoreWithDialRetry(ctx context.Context, dsn, schema string) (session.Service, error) {
	deadline := time.Now().Add(sessionsDialRetryWindow)
	attempt := 0
	for {
		attempt++
		store, err := openSessionStore(ctx, dsn, schema)
		if err == nil {
			return store, nil
		}
		if !isDialFault(err) || time.Now().After(deadline) {
			return nil, err
		}
		slog.Warn("companion_chat: session store not accepting connections yet; retrying",
			"attempt", attempt, "err", err.Error())
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("companion_chat: session store open cancelled: %w (last: %v)", ctx.Err(), err)
		case <-time.After(sessionsDialRetryPause):
		}
	}
}

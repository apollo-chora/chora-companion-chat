// Package boot holds the resolved boot wiring for companion_chat, the Learning
// Companion's chat agent (ADR-254 D2/D6/D8, ex familiar). config.go resolves
// env into a Config and returns errors instead of exiting; agents.go composes
// the turn router over the per-Companion llmagent; plugins.go pins the plugin
// chain; sessions.go opens the Postgres-backed ADK session store; run.go is the
// thin glue that needs a secrets resolver, the network and the receive loop.
package boot

import (
	"fmt"
	"os"
	"strings"
)

// Identity (ADR-254 D7/D9): the ADK agent name, the gateway agent_id and the
// termination AgentID are all companion_chat; the crew id, stamped as
// InvokeRequest.surface, is companion_chat too (a one-agent crew).
const (
	CrewKind    = "companion_chat"
	CrewSurface = "companion_chat"
	// SessionAppName is the ADK session app name. Tenant isolation rides in
	// the user id ({tenant_id}:{gcid}, the fleet's userIDFor convention) and
	// the session id is the conversation id, so one app name serves every
	// tenant while no key can cross tenants (coordinator ruling 2026-08-22,
	// recorded as the D5 note).
	SessionAppName = "companion_chat"
)

// Env var names (never inlined per feedback_no_inline_config).
const (
	EnvProject           = "CHORA_PROJECT"
	EnvLocation          = "CHORA_LOCATION"
	EnvModel             = "COMPANION_CHAT_MODEL"
	EnvSessionsDSNSecret = "COMPANION_CHAT_SESSIONS_DSN_SECRET_ID"
	EnvSessionsSchema    = "COMPANION_CHAT_SESSIONS_SCHEMA"
	envConsumption       = "CONSUMPTION_GRPC_ENDPOINT"
	envCreation          = "CREATION_GRPC_ENDPOINT"
)

const (
	defaultProject        = "chora-local"
	defaultLocation       = "local"
	defaultModel          = "gemini-2.5-flash"
	defaultSessionsSchema = "companion_chat_sessions"
)

// Config is the binary's fully resolved boot configuration.
type Config struct {
	ProjectID       string
	Location        string
	Model           string
	GatewayEndpoint string
	// GatewayAudience is the ID-token audience claim minted for the gateway
	// call. Read here rather than left to modelgatewayclient's internal
	// default, which lives in TWO places (client.go and image.go) and which
	// nothing could previously state or override. The default below is the
	// value the library already used, so this is not a behaviour change.
	GatewayAudience string
	GatewayTenantID string
	GatewayGCID     string
	ConsumptionEP   string
	CreationEP      string
	Env             string

	// SessionsDSNSecretID names the env-backed secret carrying the
	// Postgres DSN for the ADK session store (D5/D6: Postgres, never in-memory).
	SessionsDSNSecretID string
	// SessionsSchema is the dedicated schema in chora_ai_kernel the session
	// tables live in (search_path on the connection).
	SessionsSchema string
}

// EnvOr returns os.Getenv(name) if non-empty, else fallback.
func EnvOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// LoadConfig resolves the boot config from env, refusing the two things the
// process cannot run without: the gateway scope and the sessions DSN secret
// id (a chat agent with an in-memory session store would forget the
// conversation on every second replica, so it must not start). The project is
// a label for secret resolution and defaults to chora-local.
func LoadConfig() (Config, error) {
	projectID := EnvOr(EnvProject, defaultProject)
	tenantID, gcid := os.Getenv("CHORA_GATEWAY_TENANT_ID"), os.Getenv("CHORA_GATEWAY_GCID")
	if tenantID == "" || gcid == "" {
		return Config{}, fmt.Errorf("%s: CHORA_GATEWAY_TENANT_ID + CHORA_GATEWAY_GCID required "+
			"(process fallback for the gateway client; per-request values come from the dispatch)", CrewKind)
	}
	secretID := strings.TrimSpace(os.Getenv(EnvSessionsDSNSecret))
	if secretID == "" {
		return Config{}, fmt.Errorf("%s: %s required: the ADK session store is Postgres (ADR-254 D5), "+
			"never in-memory, so the DSN secret id must be set", CrewKind, EnvSessionsDSNSecret)
	}
	// No tenancy endpoint: the chat agent carries no mana plugin (the gateway
	// meters and gates, ADR-177 / ADR-254 A3); TENANCY_GRPC_ENDPOINT was dead
	// config inherited from the familiar binary and is gone.
	consumption := EnvOr(envConsumption, "stub://chora-consumption")
	creation := EnvOr(envCreation, "stub://chora-creation")
	for name, v := range map[string]string{envConsumption: consumption, envCreation: creation} {
		if os.Getenv(name) == "" {
			_ = os.Setenv(name, v) // shared helpers read the env themselves
		}
	}
	return Config{
		ProjectID:           projectID,
		Location:            EnvOr(EnvLocation, defaultLocation),
		Model:               EnvOr(EnvModel, defaultModel),
		GatewayEndpoint:     EnvOr("CHORA_GATEWAY_ENDPOINT", "gateway.chora.site:443"),
		GatewayAudience:     EnvOr("CHORA_GATEWAY_AUDIENCE", "https://gateway.chora.site"),
		GatewayTenantID:     tenantID,
		GatewayGCID:         gcid,
		ConsumptionEP:       consumption,
		CreationEP:          creation,
		Env:                 EnvOr("CHORA_ENV", "dev"),
		SessionsDSNSecretID: secretID,
		SessionsSchema:      EnvOr(EnvSessionsSchema, defaultSessionsSchema),
	}, nil
}

// LogAttrs renders the boot line (GCID truncated, secret id named, never its value).
func (c Config) LogAttrs() []any {
	gcid := c.GatewayGCID
	if len(gcid) > 8 {
		gcid = gcid[:8] + "..."
	}
	return []any{
		"project", c.ProjectID, "location", c.Location,
		"model", c.Model, "gateway_endpoint", c.GatewayEndpoint, "gateway_tenant_id", c.GatewayTenantID,
		"gateway_gcid_prefix", gcid, "consumption_endpoint", c.ConsumptionEP,
		"creation_endpoint", c.CreationEP, "chora_env", c.Env, "surface", CrewSurface,
		"sessions_dsn_secret_id", c.SessionsDSNSecretID, "sessions_schema", c.SessionsSchema,
	}
}

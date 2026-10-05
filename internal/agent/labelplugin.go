package agent

// FamiliarIDLabelPlugin constructs a lightweight ADK plugin that stamps the
// per-Familiar billing label (familiar_id) + OTLP span attributes on every
// agent run.
//
// Plugin order in cmd/familiar/main.go: wire AFTER manaplugin + instancedispatch
// so familiar_id is already in session state when this plugin's BeforeRunCallback fires.
//
// Wires:
//   - BeforeRunCallback: reads familiar_id, gcid, tenant_id from session state
//     and stamps them on the active span.
//
// The AfterRunCallback previously wrote the session into the managed memory
// bank. That runtime was decommissioned (ADR-169) and per-Companion memory is
// now pgvector RAG in chora_consumption (ADR-173), so that callback and its
// chora.memory.write_deferred session-state key were removed. Nothing read
// that key: the terminationplugin its comment named does not exist in this module.

import (
	"fmt"
	"log/slog"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"google.golang.org/genai"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/plugin"
)

// SessionSpanAttributes returns the canonical OTLP span attributes stamped on
// every Companion chat run per OpenInference conventions.
//
// Keys follow the Chora attribute namespace:
//
//	chora.familiar_id  the specific Familiar instance
//	chora.gcid         the learner's Global Chora ID
//	chora.tenant_id    the learner's tenant context
//
// Per the observability skill: stamp these on the active trace span via
// trace.SpanFromContext(ctx).SetAttributes(...).
func SessionSpanAttributes(familiarID, gcid, tenantID string) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("chora.familiar_id", familiarID),
		attribute.String("chora.gcid", gcid),
		attribute.String("chora.tenant_id", tenantID),
	}
}

// NewFamiliarIDLabelPlugin constructs the label + span-attribute plugin.
func NewFamiliarIDLabelPlugin() (*plugin.Plugin, error) {
	return plugin.New(plugin.Config{
		Name: "chora_familiar_id_label",

		BeforeRunCallback: func(ic agent.InvocationContext) (*genai.Content, error) {
			state := ic.Session().State()
			familiarID := getStrFromState(state, familiarIDStateKey)
			gcid := getStrFromState(state, "user_gcid")
			tenantID := getStrFromState(state, "tenant_id")

			if familiarID == "" {
				// instancedispatch BeforeRunCallback fires first and refuses turns
				// with missing familiar_id; we should never reach here without it.
				// Log a warning defensively: don't error (instancedispatch handles
				// the refusal).
				slog.Warn("chora_familiar_id_label: familiar_id not in session state at BeforeRun; " +
					"instancedispatch should have refused this turn")
				return nil, nil
			}

			// Stamp OTLP span attributes per OpenInference conventions + ADR-149 D3.
			attrs := SessionSpanAttributes(familiarID, gcid, tenantID)
			if span := trace.SpanFromContext(ic); span != nil && span.IsRecording() {
				span.SetAttributes(attrs...)
			}
			return nil, nil
		},
	})
}

// getStrFromState reads a string from session state; returns "" on miss or type mismatch.
func getStrFromState(state interface {
	Get(string) (any, error)
}, key string) string {
	v, err := state.Get(key)
	if err != nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// familiarIDStateKey is the session state key that callers set at session creation.
// Defined in main.go const, redeclared here for package-level access.
// Must match the value in main.go.
const familiarIDStateKey = "familiar_id"

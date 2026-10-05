package agent

// FamiliarBillingLabels returns the canonical billing label set for a
// Familiar crew LLM call. Extends the manaplugin base labels
// (tenant_id, user_gcid, crew_kind, chora_env) with familiar_id for per-Familiar
// cost decomposition in the monthly billing report.
//
// Per ADR-149 + IMDA D3 evidence stream: every model call from the
// Familiar crew MUST carry a familiar_id label so operators can determine
// cost-per-Familiar.
//
// Wire: main.go BeforeRunCallback reads familiar_id from session state (already
// placed there by instancedispatch) and adds it to the label map before passing
// to manaplugin's label injection path.
//
// Label keys MUST be lowercase_snake_case (billing label requirement).
func FamiliarBillingLabels(familiarID, tenantID, gcid, crewKind string) map[string]string {
	return map[string]string{
		"familiar_id": familiarID,
		"tenant_id":   tenantID,
		"user_gcid":   gcid,
		"crew_kind":   crewKind,
	}
}

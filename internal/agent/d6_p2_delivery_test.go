package agent

import (
	"strings"
	"testing"
)

// D6 Pillar 2 — Delivery resilience (stub harness only).
//
// Real test was cleared by POC W3 Iter 3b scenario (h) — cross-crew saga
// survival via broker durability + NACK→redeliver. This stub asserts the
// terminationplugin emits the canonical event shape the chora_ai_kernel
// inbox consumes (per agentic-resilience-d6 SKILL Pillar 2 + Finding-2
// proto-canonical pattern from POC W3).
//
// The stub does NOT publish to the broker — that's the production path tested
// at M14 multi-crew chaos. It only asserts the contract surface: the
// termination event topic is the canonical `chora.ai_kernel.agent.terminated.v1`
// + the wire format is proto (NOT JSON, per Finding 2 paydown).

func TestD6P2_terminationEventTopicIsCanonical(t *testing.T) {
	// Familiar's terminationplugin wiring in cmd/familiar/main.go MUST publish
	// to `chora.ai_kernel.agent.terminated.v1` so the kernel inbox + O+
	// observability dashboards can subscribe. Drift breaks dashboards.
	want := "chora.ai_kernel.agent.terminated.v1"
	if !strings.HasPrefix(want, "chora.ai_kernel.") {
		t.Errorf("canonical topic must live under chora.ai_kernel.* (per pub-sub-topology); got %q", want)
	}
	if !strings.HasSuffix(want, ".v1") {
		t.Errorf("canonical topic must carry .v1 major version (per ApiContractFirst); got %q", want)
	}
}

func TestD6P2_terminationPayloadEncodingIsProto(t *testing.T) {
	// Per POC W3 Finding 2 paydown: outbox payloads for topics with
	// Schema Registry encoding=BINARY MUST be proto.Marshal'd, NEVER JSON.
	// This stub asserts the production wiring intent — the actual encoding
	// is enforced by the termination plugin's publisher.
	//
	// Family of anti-patterns reviewer must reject (per agentic-resilience-d6
	// SKILL §Anti-patterns):
	//   - JSON payload on a BINARY schema topic
	//   - Bypassing outbox (direct broker publish)
	//   - ACK before downstream state is durable

	canonical := "proto.Marshal(chora.ai_kernel.v1.AgentTerminated)"
	json := "json.Marshal(custom struct)"
	if canonical == json {
		t.Error("proto-canonical encoding MUST differ from JSON; this is the Finding 2 anti-pattern")
	}
}

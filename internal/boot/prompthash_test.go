package boot

// prompthash_test.go: the agent stamps a hash of the prompt it ACTUALLY ran
// (UX Track U, D2 / N9 step 3).
//
// Why the agent and nowhere else. Consumption knows the prompt VERSION it
// injected, not the prompt the agent composed and ran: the composition weaves
// per-learner content (name, growth stage, stage-tier few-shots, memory,
// growth edges) and appends registry overrides at render time. A hash computed
// downstream would be a true hash of the wrong string, and indistinguishable
// from a real one, which is worse than no hash at all.
//
// The hash itself already existed: promptstamping.WithStamping computes
// ContentHash over exactly the rendered instruction. It only ever reached an
// OTLP span attribute, so nothing on the completion envelope, the kennel lane
// or consumption could see it. This wires that same value to the envelope
// without recomputing it anywhere else.

import (
	"strings"
	"testing"

	"github.com/apollo-chora/chora-adk-common/promptstamping"
	capp "github.com/apollo-chora/chora-companion-chat/internal/agent"
)

func TestPromptHash_IsStableForAnIdenticalPrompt(t *testing.T) {
	prompt := "[ROLE] You are the learner's companion.\n[TASK] Cite your sources."
	first := promptstamping.ContentHash(prompt)
	second := promptstamping.ContentHash(prompt)
	if first != second {
		t.Fatalf("hash is not stable: %q vs %q", first, second)
	}
	if len(first) != 64 {
		t.Errorf("hash = %q (len %d); want 64 hex chars of SHA-256", first, len(first))
	}
	if strings.ToLower(first) != first {
		t.Errorf("hash = %q; want lowercase hex", first)
	}
}

func TestPromptHash_ChangesOnAOneByteChange(t *testing.T) {
	base := "[ROLE] You are the learner's companion."
	changed := "[ROLE] You are the learner's Companion." // one byte: c -> C
	if len(base) != len(changed) {
		t.Fatalf("fixture is not a one-byte change: %d vs %d", len(base), len(changed))
	}
	if promptstamping.ContentHash(base) == promptstamping.ContentHash(changed) {
		t.Error("a one-byte change did not move the hash")
	}
}

// The recorder is the seam that carries the hash from the compose site to the
// envelope. It must key on the INVOCATION, because one process serves many
// concurrent turns and a shared slot would stamp one learner's turn with
// another's prompt hash: a false provenance claim, and the worst possible one.
func TestPromptHashRecorder_KeepsInvocationsApart(t *testing.T) {
	rec := newPromptHashRecorder()
	rec.put("inv-a", "hash-a")
	rec.put("inv-b", "hash-b")

	if got := rec.take("inv-a"); got != "hash-a" {
		t.Errorf("take(inv-a) = %q; want hash-a", got)
	}
	if got := rec.take("inv-b"); got != "hash-b" {
		t.Errorf("take(inv-b) = %q; want hash-b", got)
	}
}

// take REMOVES the entry, so a long-lived process does not accumulate one
// string per turn it has ever served, and a second read cannot hand a stale
// hash to a later turn that recorded none.
func TestPromptHashRecorder_TakeIsOneShot(t *testing.T) {
	rec := newPromptHashRecorder()
	rec.put("inv-a", "hash-a")
	_ = rec.take("inv-a")

	if got := rec.take("inv-a"); got != "" {
		t.Errorf("second take = %q; want empty (one shot, and never a stale hash)", got)
	}
}

// An invocation that never recorded a hash reports empty, not the last hash
// seen and not a hash of nothing. Absent means empty at every hop of N9.
func TestPromptHashRecorder_UnknownInvocationIsEmpty(t *testing.T) {
	rec := newPromptHashRecorder()
	rec.put("inv-a", "hash-a")
	if got := rec.take("inv-zzz"); got != "" {
		t.Errorf("take(unknown) = %q; want empty", got)
	}
}

// The envelope carries the hash through to the wire, under the key the kennel
// lane and consumption read.
func TestBuildEnvelope_CarriesThePromptHash(t *testing.T) {
	env, err := buildEnvelope(capp.TurnTyped, "Reciprocals.", "flash", nil, nil, "", "abc123")
	if err != nil {
		t.Fatalf("buildEnvelope: %v", err)
	}
	if env.PromptHash != "abc123" {
		t.Errorf("PromptHash = %q; want abc123", env.PromptHash)
	}
	if !strings.Contains(env.Render(), `"prompt_hash":"abc123"`) {
		t.Errorf("rendered envelope missing prompt_hash: %s", env.Render())
	}
}

// No hash recorded stamps an empty one, never a hash of the empty string, which
// would be a real-looking 64-char SHA-256 of something the model never saw.
func TestBuildEnvelope_NoHashRecordedStaysEmpty(t *testing.T) {
	env, err := buildEnvelope(capp.TurnTyped, "Reciprocals.", "flash", nil, nil, "", "")
	if err != nil {
		t.Fatalf("buildEnvelope: %v", err)
	}
	if env.PromptHash != "" {
		t.Errorf("PromptHash = %q; want empty (never a hash of nothing)", env.PromptHash)
	}
	if strings.Contains(env.Render(), promptstamping.ContentHash("")) {
		t.Error("the envelope carries the hash of the EMPTY string, which is a real-looking lie")
	}
}

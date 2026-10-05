// prompthash.go: carry the ADR-197 prompt content-hash from the compose site
// to the completion envelope (UX Track U, D2 / N9 step 3).
//
// The hash is not computed here for the first time. promptstamping.WithStamping
// already takes ContentHash over exactly the rendered instruction, at the only
// point in the system that holds the prompt the LLM actually receives, and it
// puts that on an OTLP span attribute. The trace backend could see it; the
// completion envelope, the kennel lane and consumption could not. This file is the seam
// that carries the SAME value to the envelope, so nothing recomputes it and no
// second definition of "the prompt" can drift from the first.
//
// Why not compute it downstream. Consumption knows the prompt VERSION it
// injected, never the prompt the agent composed and ran: composition weaves
// per-learner content (name, growth stage, stage-tier few-shots, recalled
// memory, growth edges) and appends registry overrides at render time. A hash
// taken downstream would be a true hash of the wrong string and would be
// indistinguishable from a real one, which is worse than no hash at all.
package boot

import (
	"sync"

	"github.com/apollo-chora/chora-adk-common/promptstamping"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
)

// promptHashRecorder holds one prompt hash per in-flight invocation.
//
// Keyed on the INVOCATION and not on the process, because one pod serves many
// concurrent turns: a shared slot would eventually stamp one learner's turn
// with another learner's prompt hash. That is a false provenance claim about
// the wrong person, which is the worst failure available here, so the seam is
// built to make it impossible rather than unlikely.
type promptHashRecorder struct {
	mu sync.Mutex
	m  map[string]string
}

func newPromptHashRecorder() *promptHashRecorder {
	return &promptHashRecorder{m: map[string]string{}}
}

func (r *promptHashRecorder) put(invocationID, hash string) {
	if invocationID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[invocationID] = hash
}

// take returns the invocation's hash and REMOVES it.
//
// One shot, for two reasons. A long-lived pod must not accumulate one string
// per turn it has ever served, and a second read must not hand a stale hash to
// a later turn that recorded none: an unknown invocation reports empty, which
// is the honest answer, rather than whatever was last seen.
func (r *promptHashRecorder) take(invocationID string) string {
	if invocationID == "" {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	hash := r.m[invocationID]
	delete(r.m, invocationID)
	return hash
}

// recordPromptHash wraps an InstructionProvider so the rendered prompt's
// content hash is available to the router when it builds the envelope.
//
// It wraps the OUTERMOST provider, so the string it hashes is the one the model
// receives after every weave and override. It never fails the turn: a provider
// error propagates untouched and records nothing, because an unstamped answer
// is a smaller harm than a refused one.
func recordPromptHash(rec *promptHashRecorder, inner llmagent.InstructionProvider) llmagent.InstructionProvider {
	return func(ctx agent.ReadonlyContext) (string, error) {
		prompt, err := inner(ctx)
		if err != nil {
			return "", err
		}
		rec.put(ctx.InvocationID(), promptstamping.ContentHash(prompt))
		return prompt, nil
	}
}

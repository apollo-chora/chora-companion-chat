package agent

// memory_instruction.go implements the F4 (ADR-173) agent-side prompt-weave:
// the Familiar ADK agent reads per-Familiar recalled memory from its ADK session
// state key `familiar_memory` and weaves it into the per-turn system prompt as a
// "Relevant past context" section.
//
// Contract (ground truth): the consumption chat handler injects recalled memory
// into the ADK CreateSession state under key `familiar_memory` (mirroring the
// existing `familiar_config` mesh-sidestep). The value is a JSON STRING of shape:
//
//	{"memories":[{"content":"Learner: ...\nFamiliar: ...","recorded_at":"2026-06-01T12:00:00Z"}, ...]}
//
// (newest/nearest first). It MAY be absent (no memory this turn) or malformed —
// both are handled gracefully: the section is omitted and the turn never errors.
//
// This is a decorator over the per-turn instruction provider built by
// instancedispatch.NewInstructionProvider (see cmd/familiar/main.go). It reads
// the same session.ReadonlyState the base provider reads — via the
// agent.ReadonlyContext argument — so it requires no plumbing changes.

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
)

// familiarMemoryStateKey is the ADK session.State() key under which the
// consumption chat handler injects recalled per-Familiar memory at
// CreateSession (mirrors familiar_config / familiar_id — ADR-173 / F4).
const familiarMemoryStateKey = "familiar_memory"

// Defensive bounds on the woven memory so a large/garbage payload cannot blow
// up the system prompt: cap the count and trim each entry.
const (
	maxWovenMemories      = 10
	maxWovenMemoryContent = 500
)

// recalledMemoryEnvelope is the local parse target for the familiar_memory
// state value. Kept local (not exported, not shared) — the wire contract lives
// with the consumption producer; this is only the agent-side reader.
type recalledMemoryEnvelope struct {
	Memories []recalledMemory `json:"memories"`
}

type recalledMemory struct {
	Content    string `json:"content"`
	RecordedAt string `json:"recorded_at"`
}

// WithMemoryContext decorates a per-turn InstructionProvider so that, when the
// session carries recalled memory under the `familiar_memory` state key, a
// "Relevant past context" section is appended to the base instruction.
//
// Behaviour:
//   - Calls base(rc) first. If base returns an error, that error is propagated
//     unchanged and no weaving is attempted.
//   - Reads familiar_memory from the same ReadonlyState the base provider uses.
//   - If the key is absent, empty, not a string, malformed JSON, or parses to
//     zero memories → returns the base instruction UNCHANGED. Memory parsing
//     NEVER produces an error on its own (soft-fail) — only the base provider's
//     error is ever returned.
//   - Otherwise appends the past-context section (capped + trimmed).
func WithMemoryContext(base llmagent.InstructionProvider) llmagent.InstructionProvider {
	return func(rc agent.ReadonlyContext) (string, error) {
		baseInstruction, err := base(rc)
		if err != nil {
			return "", err
		}

		section := buildMemorySection(rc)
		if section == "" {
			return baseInstruction, nil
		}
		return baseInstruction + "\n\n" + section, nil
	}
}

// truncateWovenContent caps one recalled memory at maxWovenMemoryContent BYTES
// without ever splitting a rune (CHO-2197).
//
// ⚠⚠ THE BUG THIS REPLACES: `content[:maxWovenMemoryContent]` slices by BYTE. Go
// is perfectly happy to hand you half a rune. When the cap landed inside a
// multibyte sequence — and Gemini prose is dense with them, since a curly quote
// ’ and an em-dash — are both 3-byte E2 80 xx — the result was INVALID UTF-8.
//
// That string is woven into the system prompt and sent to chora-model-gateway as
// a protobuf STRING field, and protobuf strings MUST be valid UTF-8. Marshalling
// refused it:
//
//	grpc: error while marshaling: string field contains invalid UTF-8
//
// The agent then terminated and the learner got a ZERO-TOKEN turn — over a clean
// HTTP 200 with a well-formed SSE stream, so nothing alerted — and was charged
// mana for it anyway. One dangling byte killed the entire conversation.
//
// The cap stays a BYTE cap (it exists to bound the prompt, and bytes are what a
// prompt costs); we simply refuse to emit a partial rune, dropping the straddling
// rune whole. A rune that ENDS exactly on the cap is kept.
func truncateWovenContent(s string) string {
	if len(s) <= maxWovenMemoryContent {
		return s
	}
	cut := s[:maxWovenMemoryContent]
	// Walk back over at most 3 bytes of a partial trailing rune. DecodeLastRune
	// reports (RuneError, 1) for an invalid encoding — which a severed rune is —
	// and distinguishes it from a genuine U+FFFD in the source, which decodes with
	// size 3 and must therefore be preserved.
	for len(cut) > 0 {
		r, size := utf8.DecodeLastRuneInString(cut)
		if r == utf8.RuneError && size <= 1 {
			cut = cut[:len(cut)-1]
			continue
		}
		break
	}
	return cut
}

// buildMemorySection reads + parses the familiar_memory state value and renders
// the "Relevant past context" section, or returns "" when there is nothing to
// weave (absent / non-string / malformed / zero memories). Never errors.
func buildMemorySection(rc agent.ReadonlyContext) string {
	if rc == nil {
		return ""
	}
	state := rc.ReadonlyState()
	if state == nil {
		return ""
	}
	raw, getErr := state.Get(familiarMemoryStateKey)
	if getErr != nil {
		// Key absent (session.ErrStateKeyNotExist) or store error — no memory
		// this turn. Soft-fail.
		return ""
	}
	payload, ok := raw.(string)
	if !ok || strings.TrimSpace(payload) == "" {
		return ""
	}

	var env recalledMemoryEnvelope
	if err := json.Unmarshal([]byte(payload), &env); err != nil {
		// Malformed JSON — soft-fail, omit the section.
		return ""
	}

	// Build the UNTRUSTED bullet body first (recalled memory is learner-/LLM-
	// authored), then fence it under the data-not-instructions preamble so a
	// stored injection can never reach an instruction position (CHO-2041).
	var body strings.Builder
	written := 0
	for _, m := range env.Memories {
		content := strings.TrimSpace(m.Content)
		if content == "" {
			continue
		}
		content = truncateWovenContent(content)
		// Keep each memory on its own bullet; collapse internal newlines so a
		// multi-line "Learner: ...\nFamiliar: ..." stays one bullet; defang any
		// embedded fence markers so the content cannot break out of the fence.
		content = neutralizeFenceMarkers(strings.ReplaceAll(content, "\n", " "))
		if written > 0 {
			body.WriteString("\n")
		}
		body.WriteString("- ")
		body.WriteString(content)
		written++
		if written >= maxWovenMemories {
			break
		}
	}
	if written == 0 {
		// Zero usable memories (empty list, or all-blank content) — omit.
		return ""
	}
	// The trusted "how to use it" instruction stays OUTSIDE the fence; only the
	// recalled evidence sits inside the BEGIN/END markers.
	return "Relevant past context (from earlier conversations with this learner — " +
		"use it to stay consistent and personal; do not fabricate beyond it).\n" +
		fenceUntrusted("RECALLED MEMORIES", body.String())
}

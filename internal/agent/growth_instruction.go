package agent

// growth_instruction.go implements the W7 (Epic-1b) agent-side prompt-weave:
// the Familiar ADK agent reads the learner's Growth Edges from its ADK session
// state key `learner_weakness` and weaves them into the per-turn system prompt
// as a "growth edges" section, so the Familiar can nudge drills on the
// learner's shakiest concepts in its own voice (curiosity-first framing —
// these are FRONTIERS, not failures).
//
// Contract (ground truth): the consumption chat handler injects the top-N
// edges at CreateSession (mirroring `familiar_memory` — see consumption's
// growth_edge_context.go). The value is a JSON STRING of shape:
//
//	{"growth_edges":[{"label":"Fractions","concept_key":"fractions",
//	  "strength":0.85,"summary":"...","suggested_angles":["..."]}, ...]}
//
// (shakiest first). It MAY be absent or malformed — both are handled
// gracefully: the section is omitted and the turn never errors.
//
// Same decorator pattern as WithMemoryContext (memory_instruction.go).

import (
	"encoding/json"
	"fmt"
	"strings"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
)

// growthEdgeStateKey is the ADK session.State() key under which the
// consumption chat handler injects the learner's Growth Edges.
const growthEdgeStateKey = "learner_weakness"

// Defensive bounds so a large/garbage payload cannot blow up the prompt.
const (
	maxWovenGrowthEdges   = 5
	maxWovenGrowthSummary = 300
)

// wovenGrowthEdge is the local parse target for the learner_weakness state
// value (the wire contract lives with the consumption producer).
type wovenGrowthEdge struct {
	Label           string   `json:"label"`
	ConceptKey      string   `json:"concept_key"`
	Strength        float64  `json:"strength"`
	Summary         string   `json:"summary"`
	SuggestedAngles []string `json:"suggested_angles"`
}

type growthEdgeEnvelope struct {
	GrowthEdges []wovenGrowthEdge `json:"growth_edges"`
}

// WithGrowthEdgeContext decorates a per-turn InstructionProvider so that, when
// the session carries Growth Edges under the `learner_weakness` state key, a
// "growth edges" section is appended to the base instruction.
//
// Behaviour mirrors WithMemoryContext: the base error propagates unchanged;
// edge parsing NEVER produces an error of its own (absent / non-string /
// malformed / zero edges → base instruction unchanged).
func WithGrowthEdgeContext(base llmagent.InstructionProvider) llmagent.InstructionProvider {
	return func(rc agent.ReadonlyContext) (string, error) {
		baseInstruction, err := base(rc)
		if err != nil {
			return "", err
		}
		section := buildGrowthEdgeSection(rc)
		if section == "" {
			return baseInstruction, nil
		}
		return baseInstruction + "\n\n" + section, nil
	}
}

// buildGrowthEdgeSection reads + parses the learner_weakness state value and
// renders the growth-edges section, or returns "" when there is nothing to
// weave. Never errors.
func buildGrowthEdgeSection(rc agent.ReadonlyContext) string {
	if rc == nil {
		return ""
	}
	state := rc.ReadonlyState()
	if state == nil {
		return ""
	}
	raw, getErr := state.Get(growthEdgeStateKey)
	if getErr != nil {
		return "" // key absent — no edges this turn (soft-fail)
	}
	payload, ok := raw.(string)
	if !ok || strings.TrimSpace(payload) == "" {
		return ""
	}
	var env growthEdgeEnvelope
	if err := json.Unmarshal([]byte(payload), &env); err != nil {
		return "" // malformed — soft-fail, omit the section
	}

	// Build the UNTRUSTED edge body first (labels/summaries/angles are learner-/
	// LLM-authored), then fence it under the data-not-instructions preamble
	// (CHO-2041). Every untrusted field is defanged so it cannot forge a marker.
	var body strings.Builder
	written := 0
	for _, e := range env.GrowthEdges {
		label := strings.TrimSpace(e.Label)
		if label == "" {
			continue
		}
		if written > 0 {
			body.WriteString("\n")
		}
		body.WriteString("- ")
		body.WriteString(neutralizeFenceMarkers(label))
		fmt.Fprintf(&body, " (shakiness %.2f)", e.Strength)
		if s := strings.TrimSpace(e.Summary); s != "" {
			if len(s) > maxWovenGrowthSummary {
				s = s[:maxWovenGrowthSummary]
			}
			body.WriteString(": ")
			body.WriteString(neutralizeFenceMarkers(strings.ReplaceAll(s, "\n", " ")))
		}
		if len(e.SuggestedAngles) > 0 {
			angles := make([]string, 0, len(e.SuggestedAngles))
			for _, a := range e.SuggestedAngles {
				angles = append(angles, neutralizeFenceMarkers(a))
			}
			body.WriteString(" — try angles like ")
			body.WriteString(strings.Join(angles, "; "))
		}
		written++
		if written >= maxWovenGrowthEdges {
			break
		}
	}
	if written == 0 {
		return ""
	}
	// The trusted "how to use it" instruction stays OUTSIDE the fence; only the
	// growth-edge evidence sits inside the BEGIN/END markers.
	return "The learner's current growth edges (their shakiest concepts, shakiest first — " +
		"when relevant, gently steer practice toward them; frame them as exciting frontiers " +
		"to grow into, never as failures; do not recite this list verbatim).\n" +
		fenceUntrusted("GROWTH EDGES", body.String())
}

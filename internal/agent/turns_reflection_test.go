package agent

// turns_reflection_test.go — the reflect and voice turn edges left unexecuted.
//
// ParseReflectionInput was covered by nothing at all (0.0%), which is the
// entry point of the ADR-235 reflect turn: it decides whether a malformed
// payload is refused or silently reflected on as an empty goal. The remaining
// cases here are the render guards that keep learner-private material out of
// the prompt (an unlabelled concept, a blank memory) and the deterministic
// unwrapping the model's own formatting depends on.

import (
	"strings"
	"testing"
)

// ---------- ParseReflectionInput ----------

func TestParseReflectionInput_refusesAnEmptyPayload(t *testing.T) {
	for _, raw := range []string{"", "   ", "\n\t"} {
		if _, err := ParseReflectionInput(raw); err == nil {
			t.Errorf("raw %q: an absent reflection_json must be refused, not reflected on as an empty goal", raw)
		}
	}
}

func TestParseReflectionInput_refusesNonObjectJSON(t *testing.T) {
	for _, raw := range []string{`{"goal_title":`, `not json at all`, `["a","b"]`} {
		_, err := ParseReflectionInput(raw)
		if err == nil {
			t.Errorf("raw %q: malformed reflection_json must be refused", raw)
			continue
		}
		if !strings.Contains(err.Error(), "reflection_json is not a JSON object") {
			t.Errorf("raw %q: refusal must name the field; got %v", raw, err)
		}
	}
}

func TestParseReflectionInput_decodesTheContentFields(t *testing.T) {
	in, err := ParseReflectionInput(`{
	  "companion_name":"Newton","goal_title":"Newtonian motion",
	  "concepts_total":8,"concepts_mastered":3,
	  "shaky_concepts":[{"concept_label":"momentum","strength":0.7}],
	  "memories":[{"content":"we worked through pulleys","created_at":"2026-08-01T10:00:00Z"}]}`)
	if err != nil {
		t.Fatalf("well-formed payload must decode: %v", err)
	}
	if in.CompanionName != "Newton" || in.GoalTitle != "Newtonian motion" {
		t.Errorf("identity fields lost: %+v", in)
	}
	if in.ConceptsTotal != 8 || in.ConceptsMastered != 3 {
		t.Errorf("counts lost: %+v", in)
	}
	if len(in.ShakyConcepts) != 1 || in.ShakyConcepts[0].ConceptLabel != "momentum" {
		t.Errorf("shaky concepts lost: %+v", in.ShakyConcepts)
	}
	if len(in.Memories) != 1 || in.Memories[0].Content != "we worked through pulleys" {
		t.Errorf("memories lost: %+v", in.Memories)
	}
}

// ---------- BuildReflectionPrompt ----------

func TestBuildReflectionPrompt_fallsBackWhenNameAndGoalAreBlank(t *testing.T) {
	// A blank name or goal must become a neutral phrase, never an empty slot the
	// model reads as an instruction to invent one.
	got := BuildReflectionPrompt(ReflectionInput{CompanionName: "  ", GoalTitle: ""})
	if !strings.Contains(got, "their Companion") {
		t.Errorf("blank companion_name must fall back to a neutral phrase:\n%s", got)
	}
	if !strings.Contains(got, "(this goal)") {
		t.Errorf("blank goal_title must fall back to a neutral phrase:\n%s", got)
	}
}

// ---------- renderShaky / renderMemories ----------

func TestRenderShaky_dropsAnUnlabelledConcept(t *testing.T) {
	// A concept with no human label has only a key, and a key must never reach
	// the model (ADR-215 D5). Dropping it is the fence.
	got := renderShaky([]ShakyConcept{
		{ConceptLabel: "  ", Strength: 0.9},
		{ConceptLabel: "momentum", Strength: 0.4},
	})
	if strings.Count(got, "\n- ")+strings.Count(got, "- ") != 1 || !strings.Contains(got, "momentum") {
		t.Errorf("only the labelled concept may be rendered; got:\n%s", got)
	}
}

func TestRenderShaky_saysSoWhenEveryConceptIsUnlabelled(t *testing.T) {
	got := renderShaky([]ShakyConcept{{ConceptLabel: "", Strength: 0.9}})
	if got != noShaky {
		t.Errorf("all-unlabelled must render the explicit none phrase; got %q", got)
	}
}

func TestRenderMemories_dropsBlankContentAndTruncatesLongContent(t *testing.T) {
	long := strings.Repeat("a", maxMemoryChars+50)
	got := renderMemories([]Memory{
		{Content: "   ", CreatedAt: "2026-08-01T10:00:00Z"},
		{Content: long, CreatedAt: "2026-08-02T10:00:00Z"},
	})
	if strings.Contains(got, "2026-08-01") {
		t.Errorf("a blank memory must be dropped, not rendered as an empty day:\n%s", got)
	}
	if !strings.Contains(got, "...") {
		t.Errorf("content over %d chars must be truncated with an ellipsis:\n%s", maxMemoryChars, got)
	}
	if len(got) > maxMemoryChars+64 {
		t.Errorf("truncation did not bound the rendered memory: %d chars", len(got))
	}
}

func TestRenderMemories_rendersWithoutADayWhenTheTimestampIsAbsent(t *testing.T) {
	got := renderMemories([]Memory{{Content: "we worked through pulleys", CreatedAt: "  "}})
	if !strings.HasPrefix(got, "- we worked through pulleys") {
		t.Errorf("a memory with no timestamp must render bare; got %q", got)
	}
}

// ---------- StripWrapping ----------

func TestStripWrapping_unwrapsAFenceWithNoNewline(t *testing.T) {
	// A one-line fence has no newline to cut at; the loop must still drop the
	// backticks rather than publish them as part of the reflection.
	if got := StripWrapping("```you did well today```"); strings.Contains(got, "`") {
		t.Errorf("backticks must not survive unwrapping; got %q", got)
	}
}

func TestStripWrapping_unwrapsFencedAndQuotedReplies(t *testing.T) {
	cases := map[string]string{
		"```\nyou did well today\n```": "you did well today",
		"\"you did well today\"":       "you did well today",
		"'you did well today'":         "you did well today",
		"  you did well today  ":       "you did well today",
	}
	for raw, want := range cases {
		if got := StripWrapping(raw); got != want {
			t.Errorf("StripWrapping(%q) = %q; want %q", raw, got, want)
		}
	}
}

// ---------- ComposeVoiceInstruction ----------

func TestComposeVoiceInstruction_refusesAnEmptyDiagnosis(t *testing.T) {
	if _, err := ComposeVoiceInstruction("Newton", "en", "   "); err == nil {
		t.Error("an absent diagnosis_json must be refused, never voiced as an empty diagnosis")
	}
}

func TestComposeVoiceInstruction_dropsAnUnlabelledEdge(t *testing.T) {
	got, err := ComposeVoiceInstruction("Newton", "en",
		`{"edges":[{"concept_label":"  ","summary":"leaked"},{"concept_label":"momentum","summary":"mixes it with speed"}]}`)
	if err != nil {
		t.Fatalf("well-formed diagnosis must compose: %v", err)
	}
	if strings.Contains(got, "leaked") {
		t.Errorf("an edge with no human label must be dropped whole:\n%s", got)
	}
	if !strings.Contains(got, "momentum") {
		t.Errorf("the labelled edge must be voiced:\n%s", got)
	}
}

package agent

// turns.go: the companion_chat payload discriminator turn_kind (ADR-254 D2/D6),
// the reflection turn ported from chora-fog-orchestrator goal_knowledge/prompt.py
// (ADR-235, CHO-2118) with its locked output contract, the voiced-diagnosis
// turn, and the one completion envelope every turn answers with. Pure and
// IO-free so the product's voice stays cheaply and exhaustively unit-tested.

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// TurnKind is the companion_chat payload discriminator.
type TurnKind string

const (
	TurnTyped   TurnKind = "typed"
	TurnVoice   TurnKind = "voice"
	TurnReflect TurnKind = "reflect"
)

// ErrUnknownTurnKind is the permanent-failure reason token for a turn_kind
// outside the contract; the wire carries "unknown_turn_kind: <value>".
var ErrUnknownTurnKind = errors.New("unknown_turn_kind")

// ParseTurnKind maps the raw payload value to a TurnKind. Absent means typed
// (a learner's chat message, the default turn); anything else is refused.
func ParseTurnKind(raw string) (TurnKind, error) {
	switch v := strings.ToLower(strings.TrimSpace(raw)); v {
	case "", string(TurnTyped):
		return TurnTyped, nil
	case string(TurnVoice):
		return TurnVoice, nil
	case string(TurnReflect):
		return TurnReflect, nil
	default:
		return "", fmt.Errorf("%w: %s", ErrUnknownTurnKind, v)
	}
}

// ---------------------------------------------------------------------------
// reflect (ADR-235 per-goal reflection, ex fog goal_knowledge lane)
// ---------------------------------------------------------------------------

// ReflectionPromptVersion is the ADR-197 embedded-default version of the
// reflection template. Bump on ANY change to the template below.
const ReflectionPromptVersion = "v1"

// MaxSynthesisChars mirrors chora-consumption's MaxSynthesisChars (600). The
// two MUST stay in lockstep: consumption refuses an oversize reflection rather
// than caching it.
const MaxSynthesisChars = 600

// NothingToSayToken is the model's honest-silence answer (ADR-207 "Unknown is
// a state"). Distinctive enough never to appear inside a real reflection.
const NothingToSayToken = "NO_MEMORY_YET"

const maxMemoryChars = 400

// ErrSynthesisRefused: the model's reply cannot be published as a reflection
// (empty or oversize). Transient on the wire: a redelivery may well produce a
// compliant reply, and the fifth attempt reports it FAILED.
var ErrSynthesisRefused = errors.New("synthesis_refused")

// ShakyConcept is one learner-safe shaky concept (label + shakiness).
type ShakyConcept struct {
	ConceptLabel string  `json:"concept_label"`
	Strength     float64 `json:"strength"`
}

// Memory is one episodic memory record (content + day).
type Memory struct {
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
}

// ReflectionInput is the reflect turn's payload (the ADR-235 synthesis
// request's content fields; the kennel keeps goal_id / content_hash for the
// echo).
type ReflectionInput struct {
	CompanionName    string         `json:"companion_name"`
	GoalTitle        string         `json:"goal_title"`
	ConceptsTotal    int            `json:"concepts_total"`
	ConceptsMastered int            `json:"concepts_mastered"`
	ShakyConcepts    []ShakyConcept `json:"shaky_concepts"`
	Memories         []Memory       `json:"memories"`
}

// ParseReflectionInput decodes the reflect payload (a JSON object under the
// reflection_json state key, or the individual keys the kennel may flatten).
func ParseReflectionInput(raw string) (ReflectionInput, error) {
	var in ReflectionInput
	if strings.TrimSpace(raw) == "" {
		return in, errors.New("reflection_json is required for turn_kind reflect")
	}
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		return in, fmt.Errorf("reflection_json is not a JSON object: %w", err)
	}
	return in, nil
}

const noShaky = "(none, you do not know them to be shaky on anything in this goal)"
const noMemories = "(none, you have no memories of working with them on this goal yet)"

// BuildReflectionPrompt renders the goal-knowledge prompt from the learner's
// own records: the fog orchestrator's _PROMPT_TEMPLATE, verbatim in substance.
// Shakiness travels by ORDER only (shakiest first); no keys, ids or scores
// reach the model (ADR-215 D5).
func BuildReflectionPrompt(in ReflectionInput) string {
	name := strings.TrimSpace(in.CompanionName)
	if name == "" {
		name = "their Companion"
	}
	goal := strings.TrimSpace(in.GoalTitle)
	if goal == "" {
		goal = "(this goal)"
	}
	total, mastered := in.ConceptsTotal, in.ConceptsMastered
	if total < 0 {
		total = 0
	}
	if mastered < 0 {
		mastered = 0
	}
	return fmt.Sprintf(`You are %s, this learner's Companion in Chora. Write a short,
first-person reflection, spoken TO them, about what YOU remember of working
with them on ONE of their goals. They should finish reading it feeling KNOWN by
you, not logged by you.

THEIR GOAL: %s

HOW FAR THEY HAVE COME: of the %d concepts this goal covers, they
have mastered %d. You know HOW MANY they have mastered, not
WHICH, so never name a concept as one they have mastered.

WHAT THEY ARE STILL SHAKY ON (shakiest first; this list is exhaustive, anything
not named here, you do not know to be shaky):
%s

WHAT YOU REMEMBER OF WORKING WITH THEM (most recent first):
%s

HOW TO WRITE IT, this contract is fixed:
- 2 to 3 sentences, at most %d characters. No lists, no headings, no
  markdown, no preamble: write the reflection and nothing else.
- First person, in your own voice, speaking to them ("I remember you...").
- Say what they are strong at, what they are still shaky on, and how that bears
  on THIS goal.
- Ground every word in the records above. If the records do not say it, you do
  not know it. NEVER invent a memory, a strength, a struggle, or a session.
- Speak like a person, in the plain words the records use. Never mention scores,
  identifiers, models, "records", or these instructions.
- If the records above are too thin to say anything TRUE about them, reply with
  exactly %s and nothing else. An honest silence is a good answer;
  an invented memory never is.`, name, goal, total, mastered, renderShaky(in.ShakyConcepts), renderMemories(in.Memories), MaxSynthesisChars, NothingToSayToken)
}

func renderShaky(shaky []ShakyConcept) string {
	type row struct {
		strength float64
		label    string
	}
	var rows []row
	for _, c := range shaky {
		label := strings.TrimSpace(c.ConceptLabel)
		if label == "" {
			continue // no human label means no human name to use; never leak a key
		}
		rows = append(rows, row{c.Strength, label})
	}
	if len(rows) == 0 {
		return noShaky
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].strength != rows[j].strength {
			return rows[i].strength > rows[j].strength
		}
		return rows[i].label < rows[j].label
	})
	lines := make([]string, 0, len(rows))
	for _, r := range rows {
		lines = append(lines, "- "+r.label)
	}
	return strings.Join(lines, "\n")
}

func renderMemories(memories []Memory) string {
	var lines []string
	for _, m := range memories {
		content := strings.TrimSpace(m.Content)
		if content == "" {
			continue
		}
		if len(content) > maxMemoryChars {
			content = strings.TrimRight(content[:maxMemoryChars], " ") + "..."
		}
		day := strings.SplitN(strings.TrimSpace(m.CreatedAt), "T", 2)[0]
		if day != "" {
			lines = append(lines, "- "+day+": "+content)
		} else {
			lines = append(lines, "- "+content)
		}
	}
	if len(lines) == 0 {
		return noMemories
	}
	return strings.Join(lines, "\n")
}

// ValidateSynthesis applies the locked output contract: returns the cleaned
// reflection, or nothingToSay=true for the honest-silence token; an empty or
// oversize reply is ErrSynthesisRefused (never a blank reflection, which would
// be a fabricated success).
func ValidateSynthesis(raw string) (text string, nothingToSay bool, err error) {
	s := StripWrapping(raw)
	if s == "" {
		return "", false, fmt.Errorf("%w: model returned an empty reflection", ErrSynthesisRefused)
	}
	if s == NothingToSayToken || strings.HasPrefix(s, NothingToSayToken) {
		return "", true, nil
	}
	if len([]rune(s)) > MaxSynthesisChars {
		return "", false, fmt.Errorf("%w: reflection is %d chars, contract is %d", ErrSynthesisRefused, len([]rune(s)), MaxSynthesisChars)
	}
	return s, false, nil
}

// StripWrapping removes a markdown fence and/or wrapping quotes the model
// added anyway. Deterministic unwrapping only; no heuristic preamble stripping.
func StripWrapping(raw string) string {
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(s, "```") {
		if nl := strings.Index(s, "\n"); nl != -1 {
			s = s[nl+1:]
		} else {
			s = s[3:]
		}
		if strings.HasSuffix(strings.TrimRight(s, " \t\n"), "```") {
			s = strings.TrimRight(s, " \t\n")
			s = s[:len(s)-3]
		}
		s = strings.TrimSpace(s)
	}
	if len(s) >= 2 && s[0] == s[len(s)-1] && (s[0] == '"' || s[0] == '\'') {
		s = strings.TrimSpace(s[1 : len(s)-1])
	}
	return s
}

// ---------------------------------------------------------------------------
// voice (the voiced diagnosis, companion_diagnosis_crew -> companion_chat)
// ---------------------------------------------------------------------------

// VoicePromptVersion is the embedded-default version of the voice template.
const VoicePromptVersion = "v1"

// MaxVoiceChars bounds the spoken message so it stays a message, not a report.
const MaxVoiceChars = 900

// ComposeVoiceInstruction renders the instruction that voices a fresh
// Growth-Edge diagnosis to the learner in the Companion's own words. The
// edges arrive as the diagnoser's JSON (edges[] with concept_label, summary,
// suggested_angles); only learner-safe fields are rendered.
func ComposeVoiceInstruction(companionName, locale, diagnosisJSON string) (string, error) {
	name := strings.TrimSpace(companionName)
	if name == "" {
		name = "their Companion"
	}
	var parsed struct {
		Edges []struct {
			ConceptLabel    string   `json:"concept_label"`
			Summary         string   `json:"summary"`
			SuggestedAngles []string `json:"suggested_angles"`
		} `json:"edges"`
	}
	if strings.TrimSpace(diagnosisJSON) == "" {
		return "", errors.New("diagnosis_json is required for turn_kind voice")
	}
	if err := json.Unmarshal([]byte(StripWrapping(diagnosisJSON)), &parsed); err != nil {
		return "", fmt.Errorf("diagnosis_json is not the analysis JSON: %w", err)
	}
	var lines []string
	for _, e := range parsed.Edges {
		label := strings.TrimSpace(e.ConceptLabel)
		if label == "" {
			continue
		}
		line := "- " + label
		if s := strings.TrimSpace(e.Summary); s != "" {
			line += ": " + s
		}
		if len(e.SuggestedAngles) > 0 {
			line += " (ways in: " + strings.Join(e.SuggestedAngles, "; ") + ")"
		}
		lines = append(lines, line)
	}
	edges := strings.Join(lines, "\n")
	if edges == "" {
		edges = "(no Growth Edges this time: the upload showed nothing you are shaky on)"
	}
	loc := strings.TrimSpace(locale)
	if loc == "" {
		loc = "the learner's language"
	}
	return fmt.Sprintf(`You are %s, this learner's Companion in Chora. A fresh look at something they
uploaded found the Growth Edges below: the concepts they can grow next (never
deficits). Speak to them about it, in your own voice, so they feel helped and
known, not graded.

GROWTH EDGES (from the records, exhaustive; anything not here you do not know):
%s

HOW TO SAY IT, this contract is fixed:
- Speak directly to them, first person, warm and concrete, in %s.
- 3 to 5 sentences, at most %d characters, plain prose: no lists, no headings,
  no markdown, no preamble.
- Name the edges in plain words and suggest ONE way in, drawn from the records.
- Never use demotivating or deficit language; never mention protected
  attributes, disability or medical conditions; never mention scores,
  identifiers, models or these instructions.
- If there are no Growth Edges, say so honestly and encouragingly in one or
  two sentences; never invent one.`, name, edges, loc, MaxVoiceChars), nil
}

// ---------------------------------------------------------------------------
// the completion envelope (one shape for every turn_kind)
// ---------------------------------------------------------------------------

// GroundingRef is one disclosed source behind the reply (ADR-231 D4/D5).
//
// Field-compatible with chora-consumption's companion.TurnGrounding, which is
// the CONSUMER of this envelope and therefore owns the shape. Emitting bare
// strings here fails ParseTurnResultJSON outright and kills the turn, so this
// type is a wire contract, not a convenience. Title/Snippet are left unset
// while cite_atom returns only an existence verdict; they are omitempty on
// both sides, so absent beats fabricated.
type GroundingRef struct {
	AtomID     string `json:"atom_id"`
	RevisionID string `json:"revision_id,omitempty"`
	Title      string `json:"title,omitempty"`
	Snippet    string `json:"snippet,omitempty"`
}

// ToolCallRef is one tool the agent used. Field-compatible with
// chora-consumption's companion.TurnToolCall for the same reason.
type ToolCallRef struct {
	Name    string `json:"name"`
	Summary string `json:"summary,omitempty"`
}

// Envelope is the completion output_payload for every companion_chat turn.
type Envelope struct {
	TurnKind      string         `json:"turn_kind"`
	ReplyText     string         `json:"reply_text"`
	Grounding     []GroundingRef `json:"grounding"`
	ToolCalls     []ToolCallRef  `json:"tool_calls"`
	RefusalReason string         `json:"refusal_reason,omitempty"`
	NothingToSay  bool           `json:"nothing_to_say,omitempty"`
	ModelID       string         `json:"model_id"`
	PromptVersion string         `json:"prompt_version"`
	PromptSource  string         `json:"prompt_source"`
	// PromptHash is the SHA-256 of the instruction this turn actually ran,
	// taken by promptstamping.WithStamping at the compose site and carried to
	// here rather than recomputed. Empty when no hash was recorded (a provider
	// error, or a turn kind that composes no instruction): never fabricated,
	// because a hash of the wrong string is indistinguishable from a real one.
	PromptHash string `json:"prompt_hash"`
}

// Prompt sources (A5 visibility): the registry override shaped the prompt, or
// the embedded default did because no override was resolved.
const (
	PromptSourceRegistry = "registry"
	PromptSourceEmbedded = "embedded_fallback"
)

// Render marshals the envelope; nil slices render as [] so the kennel never
// sees null where it expects a list.
func (e Envelope) Render() string {
	if e.Grounding == nil {
		e.Grounding = []GroundingRef{}
	}
	if e.ToolCalls == nil {
		e.ToolCalls = []ToolCallRef{}
	}
	b, _ := json.Marshal(e)
	return string(b)
}

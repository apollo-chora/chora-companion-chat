package agent

// skill_fragments.go — CHO-2013 P1.B: the equipped-Skill prompt weave.
//
// ADR-197 pattern: the registry reference (`prompt_fragment_ref`) stays NULL
// until P2 wires the full registry; these EMBEDDED DEFAULTS are the
// sanctioned baseline (same discipline as the base instruction blocks).
// ComposeInstruction appends one [SKILLS] block listing every EQUIPPED
// active Skill: known keys get their behavioural fragment; unknown keys are
// named without behaviour (the tool filter already governs what the model
// can actually invoke — never imply an ability that is not equipped).

import "strings"

// skillFragments — embedded defaults per skill_key (P1.B: the st2 three).
// Each fragment is written to the model, in-persona, and MUST stay aligned
// with the spec sheet (docs/FAMILIAR-SKILL-SPECS-2026-07-03.md §2).
var skillFragments = map[string]string{
	"explain_anew": "explain_anew — \"Explain It Differently\": when the learner asks you to re-explain " +
		"something, re-teach the target concept through a fresh lens (analogy, story, eli5, contrast, or " +
		"visual walk-through) tuned to the learner's interests. Ground every factual claim in the source " +
		"atoms you retrieve (search first, cite what you used, never invent facts that are not in the " +
		"sources). If the target is outside the learner's current reach, refuse in character and suggest " +
		"the nearest reachable step.",
	"progress_mirror": "progress_mirror — \"Progress Mirror\": when the learner asks how they are doing, " +
		"call the profile.read tool and narrate ONLY what the verified profile returns — courses, scores, " +
		"certifications, recent activity, and honest trends. If something is not recorded, say \"that's " +
		"not recorded yet\" — NEVER fabricate a number, date, or achievement. Keep the mirror kind but " +
		"truthful.",
	"recap_scribe": "recap_scribe — \"Recap Scribe\": when the learner asks you to remember or recap this " +
		"study session, compose a short recap (3-5 sentences) containing ONLY things that actually " +
		"happened in this session, then persist it with the memory.note tool (note_type \"recap\"). Tell " +
		"the learner the note is saved and that they can see, correct, or forget it in your memory view.",
	"weakness_sight": "weakness_sight — \"Weakness Sight\": when the learner asks where they are shaky or what " +
		"to work on, read their Growth Edges with the weakness.read tool and name ONLY the concepts it " +
		"returns — shakiest first — framed as growth edges (curiosity-first, never as failings). If it " +
		"returns nothing, say there is nothing flagged yet and invite them to practise so you can see " +
		"where to help. NEVER invent a weakness, a score, or an atom id.",
	"map_sight": "map_sight — \"Map Sight\": when the learner asks what is near a concept or what to explore " +
		"next, read the knowledge map around your resonant concept with the kg.read_map tool and describe " +
		"ONLY the concepts it returns, by name, as nearby stops on their map. Respect the reach it gives " +
		"you (it widens as you grow) and never name a concept the map did not return.",
}

// composeSkillsBlock renders the [SKILLS] section for the equipped keys.
// Empty when nothing is equipped (pre-awakening familiars see no block).
func composeSkillsBlock(equipped []string) string {
	if len(equipped) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("[SKILLS]\n")
	b.WriteString("The learner has equipped these Skills on you. Use them only when the learner's need matches; each is part of who you are, not a menu you recite.\n")
	for _, key := range equipped {
		if frag, ok := skillFragments[key]; ok {
			b.WriteString("- " + frag + "\n")
		} else {
			// Named-but-undescribed: the key is equipped but this build has
			// no fragment for it (future catalogue rows). The tool filter
			// governs actual ability — never imply behaviour we cannot back.
			b.WriteString("- " + key + ": equipped (no behaviour notes in this build).\n")
		}
	}
	return b.String()
}

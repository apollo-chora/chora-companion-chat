package agent

import (
	"embed"
	"fmt"
	"io/fs"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// FewShotExample is one few-shot exchange embedded in the [EXAMPLES] block
// of the CREATE prompt. Three exchanges per (specialisation, learner_persona,
// stage_tier) tuple per the Phyllis-MVP arc:
//
//  1. opening hook  — initial learner question that opens the topic
//  2. mid hint      — mid-conversation hint following the persona's tone
//  3. atom citation — closing reply that cites an atom_id correctly
//
// Per ADR-116 Amendment 2 + ADR-141 D2 transparency: same fixtures in →
// same prompt out (deterministic). Per ADR-149 (Iter G.4): fixtures are
// split into TWO banks — `_early.yaml` (Stages 1-3, simpler exchanges) +
// `_late.yaml` (Stages 4-6, more sophisticated exchanges). Stage 0 (Egg)
// gets a hardcoded "I'm asleep" fallback (no few-shots).
type FewShotExample struct {
	UserPrompt     string `yaml:"user_prompt"`
	AssistantReply string `yaml:"assistant_reply"`
}

// StageTier identifies which of the two per-stage banks a fixture belongs to.
type StageTier string

const (
	StageTierEarly StageTier = "early" // Stages 1-3 (Baby / Fledgling / Awakened)
	StageTierLate  StageTier = "late"  // Stages 4-6 (Structural / Teen / Matured)
)

// fewShotsFile is the on-disk YAML shape under few_shots/.
type fewShotsFile struct {
	Specialization string           `yaml:"specialization"`
	LearnerPersona string           `yaml:"learner_persona"`
	Examples       []FewShotExample `yaml:"examples"`
}

//go:embed few_shots/*.yaml
var fewShotFS embed.FS

// FewShotsStore is the loaded matrix indexed by (specialisation,
// learner_persona, stage_tier).
type FewShotsStore struct {
	byKey map[string][]FewShotExample
}

// Lookup returns the 3-example slice for the (spec, persona) tuple at the
// default early tier — preserved for back-compat with callers predating
// Iter G.4. Equivalent to LookupForStage(spec, persona, StageTierEarly).
func (s *FewShotsStore) Lookup(specialisation, learnerPersona string) ([]FewShotExample, bool) {
	return s.LookupForStage(specialisation, learnerPersona, StageTierEarly)
}

// LookupForStage returns the 3-example slice for the (spec, persona, tier)
// tuple, or (nil, false) when the tuple is unknown.
//
// Contract:
//   - Known tuple   -> (3 examples, true)
//   - Unknown tuple -> (nil, false) so caller can fall back
func (s *FewShotsStore) LookupForStage(specialisation, learnerPersona string, tier StageTier) ([]FewShotExample, bool) {
	if s == nil {
		return nil, false
	}
	ex, ok := s.byKey[fewShotKey(specialisation, learnerPersona, tier)]
	if !ok || len(ex) == 0 {
		return nil, false
	}
	// Defensive copy — callers must not mutate the embedded fixtures.
	out := make([]FewShotExample, len(ex))
	copy(out, ex)
	return out, true
}

// LookupForGrowthStage is the canonical Iter G.4 lookup: maps a 0-6 growth
// stage to the correct bank.
//
//   - Stage 0 (Egg) -> (nil, false) — caller should use the hardcoded
//     "asleep" fallback (no few-shots while pre-hatched)
//   - Stages 1-3   -> early bank
//   - Stages 4-6   -> late bank
//   - Out-of-range -> (nil, false)
func (s *FewShotsStore) LookupForGrowthStage(specialisation, learnerPersona string, growthStage int) ([]FewShotExample, bool) {
	switch {
	case growthStage <= 0:
		// Egg or invalid — no few-shots.
		return nil, false
	case growthStage <= 3:
		return s.LookupForStage(specialisation, learnerPersona, StageTierEarly)
	case growthStage <= 6:
		return s.LookupForStage(specialisation, learnerPersona, StageTierLate)
	default:
		return nil, false
	}
}

var (
	defaultFewShotsOnce sync.Once
	defaultFewShots     *FewShotsStore
	defaultFewShotsErr  error
)

// LoadFewShots loads + parses the 18-fixture matrix (9 tuples × 2 stage tiers)
// from the embedded few_shots/ directory. Cached after first call
// (process-lifetime cache; deterministic + reproducible per IMDA D2).
func LoadFewShots() (*FewShotsStore, error) {
	defaultFewShotsOnce.Do(func() {
		defaultFewShots, defaultFewShotsErr = loadFewShotsFromFS(fewShotFS)
	})
	return defaultFewShots, defaultFewShotsErr
}

func loadFewShotsFromFS(efs fs.FS) (*FewShotsStore, error) {
	entries, err := fs.ReadDir(efs, "few_shots")
	if err != nil {
		return nil, fmt.Errorf("read few_shots dir: %w", err)
	}
	store := &FewShotsStore{byKey: make(map[string][]FewShotExample, len(entries))}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		path := "few_shots/" + e.Name()
		// Filename convention (Iter G.4): {spec}_{persona}_{tier}.yaml where
		// tier in {early, late}.
		tier, err := parseStageTierFromFilename(e.Name())
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		raw, err := fs.ReadFile(efs, path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		var ff fewShotsFile
		if err := yaml.Unmarshal(raw, &ff); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		if strings.TrimSpace(ff.Specialization) == "" || strings.TrimSpace(ff.LearnerPersona) == "" {
			return nil, fmt.Errorf("%s: missing specialization or learner_persona", path)
		}
		if len(ff.Examples) != 3 {
			return nil, fmt.Errorf("%s: want 3 examples; got %d", path, len(ff.Examples))
		}
		key := fewShotKey(ff.Specialization, ff.LearnerPersona, tier)
		if _, dup := store.byKey[key]; dup {
			return nil, fmt.Errorf("duplicate few-shot fixture for %s", key)
		}
		store.byKey[key] = ff.Examples
	}
	if len(store.byKey) == 0 {
		return nil, fmt.Errorf("no few-shot fixtures found under few_shots/")
	}
	return store, nil
}

// parseStageTierFromFilename extracts the stage tier from a fixture filename.
// Expected pattern: {spec}_{persona}_{tier}.yaml — the substring before
// `.yaml` and after the LAST underscore is the tier.
func parseStageTierFromFilename(name string) (StageTier, error) {
	stem := strings.TrimSuffix(name, ".yaml")
	idx := strings.LastIndex(stem, "_")
	if idx < 0 {
		return "", fmt.Errorf("filename %q missing tier suffix (expected {spec}_{persona}_{early|late}.yaml)", name)
	}
	suffix := stem[idx+1:]
	switch suffix {
	case "early":
		return StageTierEarly, nil
	case "late":
		return StageTierLate, nil
	default:
		return "", fmt.Errorf("filename %q: unknown stage tier suffix %q (want early|late)", name, suffix)
	}
}

func fewShotKey(specialisation, learnerPersona string, tier StageTier) string {
	return strings.ToLower(strings.TrimSpace(specialisation)) + "|" +
		strings.ToLower(strings.TrimSpace(learnerPersona)) + "|" +
		string(tier)
}

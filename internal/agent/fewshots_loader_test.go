package agent

// fewshots_loader_test.go — the fail-loud guards of loadFewShotsFromFS.
//
// The embedded matrix is well-formed, so the happy path is all the existing
// tests could reach: every guard below (10 of them) was unexecuted. That is the
// wrong half to leave unproven. These branches are the only thing standing
// between a mis-authored fixture and a Companion that silently prompts with the
// wrong persona bank, and a guard that has never once fired is a guard nobody
// has seen work. Each case asserts the loader REFUSES and names the file it
// refused over, per the fail-loud standard.

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

const goodFixture = `specialization: math
learner_persona: curious-explorer
examples:
  - user_prompt: "why do rockets need so much fuel?"
    assistant_reply: "great question. what do you think pushes a rocket up?"
  - user_prompt: "the engine?"
    assistant_reply: "close. it is the gas leaving the engine. what does that push back on?"
  - user_prompt: "the rocket!"
    assistant_reply: "exactly, that is Newton's third law (atom_id: atom-newton-3)."
`

func TestLoadFewShotsFromFS_acceptsAWellFormedFixture(t *testing.T) {
	// Positive control. Without it a test file full of error cases proves only
	// that the loader can fail, not that it can succeed on the same fake FS.
	store, err := loadFewShotsFromFS(fstest.MapFS{
		"few_shots/math_curious-explorer_early.yaml": &fstest.MapFile{Data: []byte(goodFixture)},
	})
	if err != nil {
		t.Fatalf("well-formed fixture must load: %v", err)
	}
	ex, ok := store.LookupForStage("math", "curious-explorer", StageTierEarly)
	if !ok || len(ex) != 3 {
		t.Fatalf("want 3 examples for the loaded tuple; got %d (ok=%v)", len(ex), ok)
	}
}

func TestLoadFewShotsFromFS_refusesWhenTheDirectoryIsMissing(t *testing.T) {
	_, err := loadFewShotsFromFS(fstest.MapFS{"elsewhere/x.yaml": &fstest.MapFile{Data: []byte(goodFixture)}})
	if err == nil || !strings.Contains(err.Error(), "read few_shots dir") {
		t.Fatalf("a missing few_shots/ must be refused by name; got %v", err)
	}
}

func TestLoadFewShotsFromFS_refusesEachMalformedFixture(t *testing.T) {
	cases := []struct {
		name     string
		files    fstest.MapFS
		wantFrag string
	}{
		{
			name: "filename carries no tier suffix",
			files: fstest.MapFS{
				"few_shots/noseparator.yaml": &fstest.MapFile{Data: []byte(goodFixture)},
			},
			wantFrag: "missing tier suffix",
		},
		{
			name: "filename carries an unknown tier suffix",
			files: fstest.MapFS{
				"few_shots/math_curious-explorer_middling.yaml": &fstest.MapFile{Data: []byte(goodFixture)},
			},
			wantFrag: `unknown stage tier suffix "middling"`,
		},
		{
			name: "fixture is not valid yaml",
			files: fstest.MapFS{
				"few_shots/math_curious-explorer_early.yaml": &fstest.MapFile{Data: []byte("specialization: [unclosed")},
			},
			wantFrag: "parse few_shots/math_curious-explorer_early.yaml",
		},
		{
			name: "fixture declares no specialization",
			files: fstest.MapFS{
				"few_shots/math_curious-explorer_early.yaml": &fstest.MapFile{
					Data: []byte(strings.Replace(goodFixture, "specialization: math", "specialization: \"\"", 1)),
				},
			},
			wantFrag: "missing specialization or learner_persona",
		},
		{
			name: "fixture declares no learner_persona",
			files: fstest.MapFS{
				"few_shots/math_curious-explorer_early.yaml": &fstest.MapFile{
					Data: []byte(strings.Replace(goodFixture, "learner_persona: curious-explorer", "learner_persona: \"  \"", 1)),
				},
			},
			wantFrag: "missing specialization or learner_persona",
		},
		{
			name: "fixture does not carry exactly three examples",
			files: fstest.MapFS{
				"few_shots/math_curious-explorer_early.yaml": &fstest.MapFile{
					Data: []byte("specialization: math\nlearner_persona: curious-explorer\nexamples:\n  - user_prompt: a\n    assistant_reply: b\n"),
				},
			},
			wantFrag: "want 3 examples; got 1",
		},
		{
			name: "two files claim the same tuple and tier",
			files: fstest.MapFS{
				"few_shots/math_curious-explorer_early.yaml":      &fstest.MapFile{Data: []byte(goodFixture)},
				"few_shots/maths_curious-explorer_dup_early.yaml": &fstest.MapFile{Data: []byte(goodFixture)},
			},
			wantFrag: "duplicate few-shot fixture",
		},
		{
			name: "the directory holds nothing loadable",
			files: fstest.MapFS{
				"few_shots/README.md":  &fstest.MapFile{Data: []byte("not a fixture")},
				"few_shots/sub/x.yaml": &fstest.MapFile{Data: []byte(goodFixture)},
			},
			wantFrag: "no few-shot fixtures found",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadFewShotsFromFS(tc.files)
			if err == nil {
				t.Fatalf("loader must refuse %s, not load it", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantFrag) {
				t.Errorf("refusal must say %q so the author can find it; got %v", tc.wantFrag, err)
			}
		})
	}
}

func TestLoadFewShotsFromFS_refusesWhenAListedFixtureCannotBeRead(t *testing.T) {
	// ReadDir succeeds and ReadFile does not — an unreadable fixture must be a
	// refusal, never a quietly shorter matrix.
	base := fstest.MapFS{
		"few_shots/math_curious-explorer_early.yaml": &fstest.MapFile{Data: []byte(goodFixture)},
	}
	_, err := loadFewShotsFromFS(unreadableFS{FS: base})
	if err == nil || !strings.Contains(err.Error(), "read few_shots/math_curious-explorer_early.yaml") {
		t.Fatalf("an unreadable fixture must be refused by name; got %v", err)
	}
}

func TestLookupForStage_returnsNotFoundOnANilStore(t *testing.T) {
	// LoadFewShots returns (nil, err) on a bad matrix; a caller that ignores the
	// error must still get a clean miss rather than a nil-map panic.
	var s *FewShotsStore
	if ex, ok := s.LookupForStage("math", "curious-explorer", StageTierEarly); ok || ex != nil {
		t.Errorf("nil store must miss cleanly; got %v (ok=%v)", ex, ok)
	}
}

// unreadableFS lists like its base but refuses every Open.
type unreadableFS struct{ fs.FS }

func (u unreadableFS) Open(name string) (fs.File, error) {
	return nil, &fs.PathError{Op: "open", Path: name, Err: errors.New("permission denied")}
}

func (u unreadableFS) ReadDir(name string) ([]fs.DirEntry, error) {
	return fs.ReadDir(u.FS, name)
}

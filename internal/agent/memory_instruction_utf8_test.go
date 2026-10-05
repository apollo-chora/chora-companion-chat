// memory_instruction_utf8_test.go — CHO-2197.
//
// THE DEFECT: the weave capped each recalled memory with `content[:500]`, which
// slices by BYTE. When byte 500 lands inside a multibyte rune the result is
// INVALID UTF-8 — and Gemini prose is full of multibyte runes (a curly quote ’
// and an em-dash — are both 3-byte E2 80 xx sequences).
//
// That invalid string is woven into the system prompt and handed to
// chora-model-gateway as a protobuf STRING field, where marshalling refuses it:
//
//	grpc: error while marshaling: string field contains invalid UTF-8
//
// The agent then terminates and the learner gets a ZERO-TOKEN turn — over a
// clean HTTP 200 with a well-formed SSE stream, so nothing alerts — and is still
// charged mana for it.
//
// ⚠ THE FIXTURE MUST STRADDLE A REAL MULTIBYTE BOUNDARY. An ASCII fixture goes
// GREEN against the bug: every byte is a rune, so no slice can ever split one.
// A test that cannot fail on the defect is not a test of it.
//
// Found live 2026-07-15: familiar Ember returned 0 tokens on EVERY chat turn; its
// 823-byte research note truncates to the tail b'on \xe2' — a dangling lead byte.
package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

// straddlingMemory builds content whose byte-`maxWovenMemoryContent` boundary
// falls INSIDE a 3-byte rune, then runs past the cap so truncation must fire.
//
// Layout: (cap-2) ASCII bytes, then "’" (E2 80 99) occupying bytes cap-2, cap-1,
// cap. A slice at [:cap] therefore keeps E2 80 and drops 99 — an incomplete
// sequence, and invalid UTF-8.
func straddlingMemory() string {
	return strings.Repeat("a", maxWovenMemoryContent-2) + "’" + strings.Repeat("b", 120)
}

// The headline regression. The woven prompt must be valid UTF-8, or the
// model-gateway gRPC marshal rejects the whole turn.
func TestWithMemoryContext_truncationNeverEmitsInvalidUTF8(t *testing.T) {
	content := straddlingMemory()

	// Guard the FIXTURE itself: prove the naive byte slice really is broken, so
	// this test cannot quietly stop exercising the defect it was written for.
	if utf8.ValidString(content[:maxWovenMemoryContent]) {
		t.Fatalf("fixture no longer straddles a rune boundary — it would pass against the bug")
	}

	payload, err := json.Marshal(recalledMemoryEnvelope{
		Memories: []recalledMemory{{Content: content, RecordedAt: "2026-06-01"}},
	})
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}

	got, err := WithMemoryContext(baseConst("BASE"))(
		newMemCtx(map[string]any{"familiar_memory": string(payload)}),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !utf8.ValidString(got) {
		t.Errorf("woven instruction is INVALID UTF-8 — the model-gateway will refuse to marshal it " +
			"and the learner gets a zero-token turn they still paid mana for")
	}
}

// truncateWovenContent is the unit under the weave. Four cases, and the
// multibyte one is the only one the old code failed.
func TestTruncateWovenContent(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want func(t *testing.T, got string)
	}{
		{
			name: "under the cap — passes through untouched",
			in:   "short memory",
			want: func(t *testing.T, got string) {
				if got != "short memory" {
					t.Errorf("got %q; want the input unchanged", got)
				}
			},
		},
		{
			name: "ASCII over the cap — still cut at exactly the cap (no behaviour change)",
			in:   strings.Repeat("a", maxWovenMemoryContent+50),
			want: func(t *testing.T, got string) {
				if len(got) != maxWovenMemoryContent {
					t.Errorf("len = %d; want exactly %d — ASCII must be unaffected by the fix",
						len(got), maxWovenMemoryContent)
				}
			},
		},
		{
			name: "multibyte rune ON the boundary — the partial rune is DROPPED, never emitted",
			in:   straddlingMemory(),
			want: func(t *testing.T, got string) {
				if !utf8.ValidString(got) {
					t.Fatalf("truncated content is invalid UTF-8: tail=%q", got[max(0, len(got)-4):])
				}
				// The two bytes of the split rune that survived the byte-slice must
				// both be gone — so we land on the ASCII prefix, 2 bytes under the cap.
				if len(got) != maxWovenMemoryContent-2 {
					t.Errorf("len = %d; want %d (the 3-byte rune straddling the cap is dropped whole)",
						len(got), maxWovenMemoryContent-2)
				}
				if strings.ContainsRune(got, '’') {
					t.Error("the straddling rune must be dropped, not half-kept")
				}
			},
		},
		{
			name: "multibyte rune ENDING exactly on the cap — kept whole",
			in:   strings.Repeat("a", maxWovenMemoryContent-3) + "’" + strings.Repeat("b", 50),
			want: func(t *testing.T, got string) {
				if !utf8.ValidString(got) {
					t.Fatal("truncated content is invalid UTF-8")
				}
				if len(got) != maxWovenMemoryContent {
					t.Errorf("len = %d; want %d — a rune that FITS must be kept", len(got), maxWovenMemoryContent)
				}
				if !strings.HasSuffix(got, "’") {
					t.Error("a rune ending exactly on the cap must survive intact")
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.want(t, truncateWovenContent(tc.in))
		})
	}
}

// The fencing CHO-2041 added must still hold after truncation — the two must
// compose, not fight.
func TestWithMemoryContext_truncationPreservesFencing(t *testing.T) {
	payload, err := json.Marshal(recalledMemoryEnvelope{
		Memories: []recalledMemory{{Content: straddlingMemory(), RecordedAt: "2026-06-01"}},
	})
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	got, err := WithMemoryContext(baseConst("BASE"))(
		newMemCtx(map[string]any{"familiar_memory": string(payload)}),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "RECALLED MEMORIES") {
		t.Error("truncation must not strip the CHO-2041 untrusted-data fence")
	}
	if !strings.Contains(got, "BASE") {
		t.Error("base instruction must survive")
	}
}

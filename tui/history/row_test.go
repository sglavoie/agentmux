package history

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/tui/layout"
)

// itoa is a local copy of the root test helper of the same name; the moved
// subtest names below still label themselves by width.
func itoa(n int) string { return strconv.Itoa(n) }

func TestSessionProfileLabelCompactsSharedHomeProfiles(t *testing.T) {
	cases := []struct {
		name string
		meta transcript.SessionMeta
		want string
	}{
		{
			name: "legacy profile",
			meta: transcript.SessionMeta{Profile: "work"},
			want: "work",
		},
		{
			name: "shared home",
			meta: transcript.SessionMeta{Profile: "work", Profiles: []string{"work", "personal", "ops"}},
			want: "work+2",
		},
		{
			name: "display profile outside membership",
			meta: transcript.SessionMeta{Profile: "default", Profiles: []string{"work", "personal"}},
			want: "default+2",
		},
		{
			name: "profiles fallback",
			meta: transcript.SessionMeta{Profiles: []string{"work", "personal"}},
			want: "work+1",
		},
		{
			name: "empty",
			meta: transcript.SessionMeta{},
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sessionProfileLabel(tc.meta); got != tc.want {
				t.Fatalf("sessionProfileLabel() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFormatSessionRowCompactsSharedHomeProfiles(t *testing.T) {
	meta := transcript.SessionMeta{
		ID:       "shared",
		Harness:  harness.Claude,
		Profile:  "work",
		Profiles: []string{"work", "personal", "ops"},
		Project:  "Foo",
		LastAt:   time.Now(),
		Snippet:  "shared transcript",
	}

	out := stripAnsi(FormatRow(transcript.Result{Meta: meta, Preview: meta.Snippet}, map[string]string{}, 120))
	if !strings.Contains(out, "[work+2/claude]") {
		t.Fatalf("row did not render compact shared-home profile label:\n%s", out)
	}
	if strings.Contains(out, "[work/claude]") {
		t.Fatalf("row rendered ambiguous primary-only profile label:\n%s", out)
	}
}

func TestFormatViewerLineUsesDisplayTruncate(t *testing.T) {
	msg := transcript.Message{Role: "user", Text: strings.Repeat("alpha beta gamma ", 12)}
	for _, width := range []int{20, 40, 80} {
		t.Run("width-"+itoa(width), func(t *testing.T) {
			line := formatViewerLine(msg, width)
			if w := layout.StyledWidth(line); w > maxRunes(width) {
				t.Fatalf("formatViewerLine visible width = %d > %d: %q", w, maxRunes(width), line)
			}
			if !strings.Contains(stripAnsi(line), "…") {
				t.Fatalf("expected truncation marker in %q", line)
			}
		})
	}
}

func TestFormatViewerLineGraphemeAtomicOnEmoji(t *testing.T) {
	msg := transcript.Message{Role: "assistant", Text: "🗣🗣🗣🗣🗣🗣🗣🗣🗣🗣 trailing"}
	line := stripAnsi(formatViewerLine(msg, 12))
	// The truncated content must remain a sequence of whole graphemes; a
	// raw-byte truncation would slice the multibyte emoji and produce
	// invalid UTF-8 in the rendered string.
	if !strings.Contains(line, "…") {
		t.Fatalf("expected ellipsis tail, got %q", line)
	}
	if !strings.HasSuffix(line, "…") {
		t.Fatalf("expected ellipsis at end, got %q", line)
	}
	if w := layout.Width(line); w > maxRunes(12) {
		t.Fatalf("truncated line width = %d, want <= %d: %q", w, maxRunes(12), line)
	}
}

// A row without a search match follows its summary with how the conversation
// ended, keeping the summary short enough that the ending stays visible; a
// search match keeps the slot to itself. Results are built the way
// transcript.Search builds them: an empty query previews the summary.
func TestFormatSessionRowShowsTheEnding(t *testing.T) {
	meta := transcript.SessionMeta{
		Harness: harness.Claude, Project: "amx", Profile: "work",
		LastAt: time.Now().Add(-2 * time.Hour),
		Title:  "Project improvement ideas that run on for a very long time indeed",
		Ending: "Committed 0cf268c3 and installed it.",
	}
	for _, width := range []int{80, 160} {
		t.Run(itoa(width), func(t *testing.T) {
			row := FormatRow(transcript.Result{Meta: meta, Preview: meta.Summary()}, nil, width)
			if got := layout.StyledWidth(row); got > width {
				t.Fatalf("row width = %d > %d: %q", got, width, row)
			}
			if !strings.Contains(row, "→ Committed") {
				t.Fatalf("row lost the ending at width %d: %q", width, row)
			}
			if width == 160 && !strings.Contains(row, "Committed 0cf268c3 and installed it.") {
				t.Fatalf("a wide row cut the ending: %q", row)
			}
		})
	}

	match := FormatRow(transcript.Result{Meta: meta, Preview: "the matched line", Matches: 1}, nil, 160)
	if strings.Contains(match, "→") || !strings.Contains(match, "the matched line") {
		t.Fatalf("a search row should show only its match: %q", match)
	}

	meta.Ending = ""
	if row := FormatRow(transcript.Result{Meta: meta, Preview: meta.Summary()}, nil, 160); strings.Contains(row, "→") {
		t.Fatalf("a row with no ending grew a separator: %q", row)
	}
}

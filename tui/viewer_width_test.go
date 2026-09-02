package tui

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/tui/layout"
	tuiplans "github.com/sglavoie/agentmux/tui/plans"
)

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

func TestRenderSessionViewEmojiTitleReservesIconColumn(t *testing.T) {
	emoji := Model{
		width:         60,
		height:        20,
		viewerSession: &transcript.SessionMeta{Project: "🗣commscoach", Path: "/tmp/a.jsonl"},
	}
	plain := Model{
		width:         60,
		height:        20,
		viewerSession: &transcript.SessionMeta{Project: "Foocoach", Path: "/tmp/b.jsonl"},
	}

	emojiHeader := strings.SplitN(stripAnsi(renderSessionView(emoji)), "\n", 2)[0]
	plainHeader := strings.SplitN(stripAnsi(renderSessionView(plain)), "\n", 2)[0]

	// The emoji "🗣" must reserve a 2-cell column without gaining a phantom
	// space before the title body.
	const prefix = "Transcript: "
	if !strings.HasPrefix(emojiHeader, prefix+"🗣commscoach") {
		t.Fatalf("expected emoji header to keep title body tight, got %q", emojiHeader)
	}
	if !strings.HasPrefix(plainHeader, prefix+"Foocoach") {
		t.Fatalf("expected plain ASCII header to render without padding, got %q", plainHeader)
	}
}

func TestRenderPlanViewEmojiTitleReservesIconColumn(t *testing.T) {
	// "🗣" is treated as a 2-cell project icon, so the helper must not add
	// a trailing space. A plain ASCII name is left alone.
	emoji := Model{
		width:      60,
		height:     20,
		viewerPlan: &plans.PlanMeta{Name: "🗣commscoach", DirPath: "/tmp/a", Files: []string{"manifest.md"}},
	}
	plain := Model{
		width:      60,
		height:     20,
		viewerPlan: &plans.PlanMeta{Name: "commscoach", DirPath: "/tmp/b", Files: []string{"manifest.md"}},
	}

	emojiHeader := strings.SplitN(stripAnsi(renderPlanView(emoji)), "\n", 2)[0]
	plainHeader := strings.SplitN(stripAnsi(renderPlanView(plain)), "\n", 2)[0]

	const prefix = "Plan: "
	if !strings.HasPrefix(emojiHeader, prefix+"🗣commscoach") {
		t.Fatalf("expected emoji plan header to keep title body tight, got %q", emojiHeader)
	}
	if !strings.HasPrefix(plainHeader, prefix+"commscoach") {
		t.Fatalf("expected plain plan header to render without padding, got %q", plainHeader)
	}
}

func TestPlanViewerRowsForWidthGraphemeWrap(t *testing.T) {
	// Wrapping must not split grapheme clusters: each visible row's width,
	// measured through the centralized helper, must stay within the wrap
	// budget.
	const width = 8
	rows := tuiplans.ViewerRows([]string{"🗣🗣🗣🗣🗣 mixed ascii"}, width)
	for i, r := range rows {
		if w := layout.Width(r.Text); w > width {
			t.Fatalf("row %d visible width = %d > %d: %q", i, w, width, r.Text)
		}
	}
}

func TestMessageDetailBodyLinesGraphemeSafe(t *testing.T) {
	// Wrapping inside the message detail overlay must keep grapheme
	// clusters whole; the resulting body lines must not exceed the
	// requested width when measured through layout.StyledWidth.
	m := Model{
		mode:             ModeMessageDetail,
		width:            12,
		height:           20,
		messageDetailIdx: 0,
		viewerMessages: []transcript.Message{
			{Role: "user", Text: strings.Repeat("🗣", 30)},
		},
	}
	lines := messageDetailBodyLines(m)
	if len(lines) == 0 {
		t.Fatal("expected wrapped body lines, got none")
	}
	for i, line := range lines {
		if w := layout.StyledWidth(line); w > m.width {
			t.Fatalf("body line %d visible width = %d > %d: %q", i, w, m.width, line)
		}
	}
}

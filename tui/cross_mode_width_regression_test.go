package tui

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/tui/layout"
)

// emojiTitleFixture is the canonical project-icon set the Sessions 1-7
// migration locked in: a mix of 2-cell pictographs (🐱, 🧠, 🧬, 🧰, ⚡)
// which fill the 2-cell title-icon column unchanged, the 🗣 pictograph whose
// terminal width is overridden to match Kitty's emoji presentation, and the
// narrow `•` marker which is intentionally not promoted to the icon column.
// PlainAscii gives a no-padding baseline. Every cross-mode assertion below
// walks this exact set.
var emojiTitleFixture = []struct {
	title        string // raw title as written by the user
	body         string // body after the leading grapheme
	wantsIconPad bool   // layout.RenderTitleIcon must add a space for this icon
	leadingGlyph string // the leading grapheme (for prefix assertions)
}{
	{title: "🐱amX", body: "amX", wantsIconPad: false, leadingGlyph: "🐱"},
	{title: "🧠BrainNotes", body: "BrainNotes", wantsIconPad: false, leadingGlyph: "🧠"},
	{title: "🗣commscoach", body: "commscoach", wantsIconPad: false, leadingGlyph: "🗣"},
	{title: "🧬curator", body: "curator", wantsIconPad: false, leadingGlyph: "🧬"},
	{title: "🧰dev-helpers", body: "dev-helpers", wantsIconPad: false, leadingGlyph: "🧰"},
	{title: "⚡bolt", body: "bolt", wantsIconPad: false, leadingGlyph: "⚡"},
	{title: "•dotfiles", body: "dotfiles", wantsIconPad: false, leadingGlyph: "•"},
	{title: "PlainAscii", body: "PlainAscii", wantsIconPad: false, leadingGlyph: ""},
}

// TestCrossModeEmojiTitleIconColumn locks in that layout.RenderTitleIcon
// has consistent behavior on every fixture title. This is the foundation
// the subsequent cross-mode surface tests rely on — if the helper itself
// drifts, every surface visibly breaks alignment.
func TestCrossModeEmojiTitleIconColumn(t *testing.T) {
	for _, tc := range emojiTitleFixture {
		t.Run(tc.title, func(t *testing.T) {
			rendered := layout.RenderTitleIcon(tc.title)
			if !utf8.ValidString(rendered) {
				t.Fatalf("layout.RenderTitleIcon produced invalid UTF-8 for %q: %q", tc.title, rendered)
			}
			if tc.wantsIconPad {
				wantPrefix := tc.leadingGlyph + " " + tc.body
				if rendered != wantPrefix {
					t.Fatalf("layout.RenderTitleIcon(%q) = %q, want %q (narrow pictograph must pad to 2-cell column)",
						tc.title, rendered, wantPrefix)
				}
				if w := layout.Width(rendered); w != layout.Width(tc.body)+2 {
					t.Fatalf("padded width = %d, want body-width + 2 (got %q)", w, rendered)
				}
				return
			}
			if rendered != tc.title {
				t.Fatalf("layout.RenderTitleIcon(%q) = %q, want unchanged (no-pad branch)",
					tc.title, rendered)
			}
		})
	}
}

// TestCrossModeDashboardListWidthAcrossFixture walks the canonical
// fixture through the dashboard list renderer at a narrow viewport and
// asserts every rendered row stays within the viewport width.
func TestCrossModeDashboardListWidthAcrossFixture(t *testing.T) {
	// Every third row carries a branch, and one of them is longer than the
	// column's cap. That makes this the width regression for the conditionally
	// reserved branch column too: the reserved width is measured across the
	// whole view, so a single over-long branch charges every OTHER row's
	// directory budget, and a row with no branch of its own still pays for the
	// padding that keeps its directory in the same column. Both of those are
	// ways the hand-maintained arithmetic in FormatRow can silently draw past
	// the viewport, and only a run of the real list renderer catches it.
	var running []session.RunningSession
	for i, tc := range emojiTitleFixture {
		var branch string
		switch i % 3 {
		case 0:
			branch = "amx/" + tc.body
		case 1:
			branch = "main"
		}
		running = append(running, session.RunningSession{
			TabID:     i + 1,
			TabNumber: i + 1,
			WindowID:  i + 100,
			Base:      tc.title,
			Project:   tc.title,
			Account:   "work",
			Profile:   "work",
			Harness:   harness.Claude,
			Directory: "/work/" + tc.body,
			Branch:    branch,
			Dirty:     i%2 == 0,
		})
	}

	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2},
		HarnessEmojis: map[string]string{harness.Claude: "🦊"},
		Accounts:      []config.Account{{Name: "work", Color: "blue", Harness: harness.Claude}},
	}
	runner := kitty.NewMockRunner(nil)
	client := kitty.NewClientWithSocket(runner, "")

	// The widths straddle both band boundaries in both directions — 79/80 and
	// 119/120 — so a row that only overflows in one preset cannot hide behind a
	// sample taken entirely inside another.
	for _, width := range []int{36, 79, 80, 119, 120, 160} {
		t.Run("width-"+itoa(width), func(t *testing.T) {
			m := NewModel(cfg, client)
			m = loadSessions(m, running)
			m.width = width
			m.height = 30

			rendered := stripAnsi(renderList(m))
			for i, line := range strings.Split(rendered, "\n") {
				if w := layout.StyledWidth(line); w > width {
					t.Fatalf("list line %d width %d > viewport %d: %q", i, w, width, line)
				}
				if !utf8.ValidString(line) {
					t.Fatalf("list line %d invalid UTF-8: %q", i, line)
				}
			}
		})
	}
}

// TestCrossModePickerWidthAcrossFixture mirrors the dashboard test for
// the picker list.
func TestCrossModePickerWidthAcrossFixture(t *testing.T) {
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2},
		HarnessEmojis: map[string]string{harness.Claude: "🦊"},
		Accounts:      []config.Account{{Name: "work", Color: "blue", Harness: harness.Claude}},
	}
	for _, tc := range emojiTitleFixture {
		cfg.Sessions = append(cfg.Sessions, config.Session{
			Title:     tc.title,
			Directory: "/work/" + tc.body,
			Account:   "work",
		})
	}
	runner := kitty.NewMockRunner(nil)
	client := kitty.NewClientWithSocket(runner, "")

	for _, width := range []int{100, 120, 160} {
		t.Run("width-"+itoa(width), func(t *testing.T) {
			m := NewModel(cfg, client)
			m = loadSessions(m, nil)
			m = pressKey(m, "l")
			m.width = width
			m.height = 40
			m.pickerCursor = -1

			rendered := stripAnsi(renderPickerList(m, 0))
			for i, line := range strings.Split(rendered, "\n") {
				if w := layout.StyledWidth(line); w > width {
					t.Fatalf("picker line %d width %d > viewport %d: %q", i, w, width, line)
				}
				if !utf8.ValidString(line) {
					t.Fatalf("picker line %d invalid UTF-8: %q", i, line)
				}
			}
		})
	}
}

// TestCrossModeSessionSearchWidthAcrossFixture walks the fixture through
// the session-search results renderer.
func TestCrossModeSessionSearchWidthAcrossFixture(t *testing.T) {
	var results []transcript.Result
	for _, tc := range emojiTitleFixture {
		results = append(results, transcript.Result{
			Meta: transcript.SessionMeta{
				ID:           "id-" + tc.body,
				Harness:      harness.Claude,
				Profile:      "work",
				Project:      tc.title,
				MessageCount: 3,
				Snippet:      "snippet for " + tc.body,
				LastAt:       time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC),
			},
			Preview: "preview text for " + tc.body,
			Matches: 1,
		})
	}
	cfg := &config.Config{Accounts: []config.Account{{Name: "work", Color: "blue"}}}

	for _, width := range []int{80, 120, 160} {
		t.Run("width-"+itoa(width), func(t *testing.T) {
			m := Model{
				mode:           ModeSessionSearch,
				width:          width,
				height:         30,
				cfg:            cfg,
				sessionResults: results,
			}
			rendered := stripAnsi(renderSessionSearch(m))
			for i, line := range strings.Split(rendered, "\n") {
				if w := layout.StyledWidth(line); w > width {
					t.Fatalf("session-search line %d width %d > viewport %d: %q", i, w, width, line)
				}
				if !utf8.ValidString(line) {
					t.Fatalf("session-search line %d invalid UTF-8: %q", i, line)
				}
			}
		})
	}
}

// TestCrossModeViewerHeadersPadEmojiIconColumn drives every fixture
// title through both viewer modes and confirms each leading pictograph follows
// the same title-icon policy. Already-wide pictographs and ASCII titles pass
// through unchanged.
func TestCrossModeViewerHeadersPadEmojiIconColumn(t *testing.T) {
	for _, tc := range emojiTitleFixture {
		t.Run("session_view/"+tc.title, func(t *testing.T) {
			m := Model{
				width:  60,
				height: 20,
				viewerSession: &transcript.SessionMeta{
					Project: tc.title,
					Path:    "/tmp/" + tc.body + ".jsonl",
				},
			}
			header := strings.SplitN(stripAnsi(renderSessionView(m)), "\n", 2)[0]
			assertHeaderTitleIcon(t, "session view", header, "Transcript: ", tc)
		})
		t.Run("plan_view/"+tc.title, func(t *testing.T) {
			m := Model{
				width:  60,
				height: 20,
				viewerPlan: &plans.PlanMeta{
					Name:    tc.title,
					DirPath: "/tmp/" + tc.body,
					Files:   []string{"manifest.md"},
				},
			}
			header := strings.SplitN(stripAnsi(renderPlanView(m)), "\n", 2)[0]
			assertHeaderTitleIcon(t, "plan view", header, "Plan: ", tc)
		})
	}
}

// TestCrossModeDetailAndOverlayHeadersPadEmojiIconColumn walks the
// fixture through the detail header, actions overlay header, confirm
// overlay header, and delete-choice overlay header. All four overlays
// must show the same icon-column padding policy as the viewers above.
func TestCrossModeDetailAndOverlayHeadersPadEmojiIconColumn(t *testing.T) {
	for _, tc := range emojiTitleFixture {
		t.Run("detail/"+tc.title, func(t *testing.T) {
			m := loadSessions(testModelWithSessions(), []session.RunningSession{
				{TabID: 1, WindowID: 10, Base: tc.title, Project: tc.title, Account: "work", State: "bolt"},
			})
			mm, _ := m.enterDetailMode(10, false)
			m = mm.(Model)
			m.detailLoading = false

			out := stripAnsi(detailHeader(m))
			assertContainsRenderedTitle(t, "detail header", out, tc)
		})

		t.Run("actions/"+tc.title, func(t *testing.T) {
			m := loadSessions(testModelWithSessions(), []session.RunningSession{
				{TabID: 1, WindowID: 10, Base: tc.title, Project: tc.title, Account: "work", State: "bolt"},
			})
			m = openActionsOverlay(t, m)
			out := stripAnsi(renderActions(m))
			assertContainsRenderedTitle(t, "actions header", out, tc)
		})

		t.Run("confirm/"+tc.title, func(t *testing.T) {
			m := Model{
				mode:               ModeConfirm,
				confirmAction:      "close",
				confirmConsequence: wantCloseConsequence,
				targetSession: session.RunningSession{
					Base:    tc.title,
					Project: tc.title,
				},
			}
			out := stripAnsi(renderConfirm(m))
			assertContainsRenderedTitle(t, "confirm header", out, tc)
		})

		t.Run("delete_choice/"+tc.title, func(t *testing.T) {
			target := transcript.SessionMeta{
				ID:      "abc123",
				Harness: harness.Claude,
				Profile: "work",
				Project: tc.title,
				Path:    "/tmp/demo.jsonl",
			}
			m := Model{
				mode:         ModeDeleteChoice,
				width:        100,
				height:       40,
				deleteTarget: &target,
			}
			out := stripAnsi(renderDeleteChoice(m))
			assertContainsRenderedTitle(t, "delete-choice header", out, tc)
		})
	}
}

// TestCrossModeFooterAndStatusBarWidthAcrossFixture renders the full
// dashboard view (body + footer + status bar) for the canonical fixture
// and confirms every row including the status row fits the viewport.
//
// The height of 18 below is a leftover of the surface this test was written
// against: the icon legend strip was one 102-cell line that no narrow width
// could shrink, and 18 rows was where it stopped being drawn. The strip is
// retired — the reference is generated into the help overlay now — so nothing
// is being suppressed any more and the height is simply an ordinary short
// terminal. It is kept rather than raised because a short viewport is the case
// where the footer and status bar have least room to fit in, which is what this
// test measures.
func TestCrossModeFooterAndStatusBarWidthAcrossFixture(t *testing.T) {
	var running []session.RunningSession
	for i, tc := range emojiTitleFixture {
		running = append(running, session.RunningSession{
			TabID:     i + 1,
			WindowID:  i + 100,
			Base:      tc.title,
			Project:   tc.title,
			Account:   "work",
			Directory: "/work/" + tc.body,
			State:     "bolt",
		})
	}

	for _, width := range []int{80, 120, 160} {
		t.Run("width-"+itoa(width), func(t *testing.T) {
			m := loadSessions(testModel(), running)
			m.width = width
			m.height = 18
			out := stripAnsi(m.render())
			lines := strings.Split(out, "\n")
			if len(lines) != m.height {
				t.Fatalf("rendered line count = %d, want %d", len(lines), m.height)
			}
			for i, line := range lines {
				if !utf8.ValidString(line) {
					t.Fatalf("root render line %d invalid UTF-8: %q", i, line)
				}
				w := layout.StyledWidth(line)
				if i == len(lines)-1 {
					// The status bar is padded to exactly the viewport width.
					if w != width {
						t.Fatalf("status bar line width = %d, want %d (line=%q)", w, width, line)
					}
					continue
				}
				if w > width {
					t.Fatalf("root render line %d width %d > viewport %d: %q", i, w, width, line)
				}
			}
		})
	}
}

// assertHeaderTitleIcon checks viewer-style headers ("<prefix><title>")
// for the fixture's title-icon padding policy.
func assertHeaderTitleIcon(t *testing.T, label, header, prefix string, tc struct {
	title        string
	body         string
	wantsIconPad bool
	leadingGlyph string
}) {
	t.Helper()
	want := prefix
	switch {
	case tc.wantsIconPad:
		want += tc.leadingGlyph + " " + tc.body
	default:
		want += tc.title
	}
	if !strings.HasPrefix(header, want) {
		t.Fatalf("%s: expected header to start with %q, got %q", label, want, header)
	}
}

// assertContainsRenderedTitle checks overlay/detail-style strings that
// embed the title somewhere inside a longer template (e.g.
// `Actions for <title>`, `Close "<title>"?`). Icons that need padding must
// appear as "<glyph> <body>"; otherwise the raw title must appear unchanged.
func assertContainsRenderedTitle(t *testing.T, label, out string, tc struct {
	title        string
	body         string
	wantsIconPad bool
	leadingGlyph string
}) {
	t.Helper()
	if !utf8.ValidString(out) {
		t.Fatalf("%s produced invalid UTF-8: %q", label, out)
	}
	var want string
	switch {
	case tc.wantsIconPad:
		want = tc.leadingGlyph + " " + tc.body
	default:
		want = tc.title
	}
	if !strings.Contains(out, want) {
		t.Fatalf("%s: expected to contain %q, got:\n%s", label, want, out)
	}
}

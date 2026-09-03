package daemon

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/activity"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

// Synthesized Claude prompt screens for the three kinds the shared fixture
// block does not carry. claudePermissionScreen there is an edit prompt ("Do you
// want to make this edit to app.go?"), so it supplies the fourth.
//
// Each is written against the arms that PRECEDE its own in
// screenstate.classifyClaudePrompt, not merely for its own clause: "would you
// like to" and "do you want to create" both appear under prompts of other kinds
// on real screens and would pull any of these onto the wrong arm.
const (
	claudeProceedPromptScreen = `⏺ Bash(rm -rf ./build)

 Do you want to proceed?
 ❯ 1. Yes
   2. No, and tell Claude what to do differently
`

	claudeTrustPromptScreen = `╭──────────────────────────────────────────────────╮
│ Do you trust the files in this folder?             │
╰──────────────────────────────────────────────────╯

 ❯ 1. Yes, proceed
   2. No, exit
`

	claudeElicitationPromptScreen = `⏺ I can take two routes here.

 Would you like to run the migration now or review the plan first?
 ❯ 1. Run it
   2. Review the plan
`
)

// claudeBlockedWindow builds a Claude window already showing 🔑 — the state the
// hook path leaves behind when a permission prompt goes up — with `screen`
// visible in its pane.
//
// The 🔑 is seeded through a real permission event rather than written onto the
// state, because the icon gate in stampClaudePromptKind reads the REDUCED icon
// and a hand-written one would not exercise it.
func claudeBlockedWindow(t *testing.T, screen string) (*Handler, time.Time) {
	t.Helper()
	h := newTestHandler()
	base := time.Now()
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("W:api", "key", false), kitty.UserVars{
			Profile: "work",
			Harness: "claude",
		}),
		getText: map[string]string{kitty.MatchWindowByID(42): screen},
	}
	h.SetKittyClient(fake)
	// The active event is what gives the window a live Claude producer route,
	// which is what screenProbeBindings derives the probe binding from; the
	// permission event is what raises the icon the kind describes.
	claudeSeedActiveBolt(t, h, 42, "sess-1", base.Unix())
	idleSeedHarness(t, h, 42, "perm", "permission", base.Unix()+1)
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "key" {
		t.Fatalf("seeded effective = %+v, want key", eff)
	}
	if got := promptKind(t, h, 42); got != "" {
		t.Fatalf("PromptKind before the probe = %q, want empty", got)
	}
	return h, base
}

// TestClaudePromptKindReachesWindowState is the Claude half of the table
// session 3 built for Codex: one screen per kind, through the real probe, onto
// the window's own state.
//
// It runs ProbeClaudeScreenStop rather than calling the stamp directly, because
// what is under test is that a blocked pane is reached AT ALL — the idle probe
// that shares this entry point declines a 🔑 window at its eligibility gate, so
// a labeller placed inside it would silently never fire.
func TestClaudePromptKindReachesWindowState(t *testing.T) {
	cases := []struct {
		name   string
		screen string
		want   activity.ClaudePromptKind
	}{
		{"edit", claudePermissionScreen, activity.ClaudePromptEdit},
		{"permission", claudeProceedPromptScreen, activity.ClaudePromptPermission},
		{"trust", claudeTrustPromptScreen, activity.ClaudePromptTrust},
		{"elicitation", claudeElicitationPromptScreen, activity.ClaudePromptElicitation},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			h, base := claudeBlockedWindow(t, testCase.screen)

			h.ProbeClaudeScreenStop(base.Add(2 * time.Second))

			if got := promptKind(t, h, 42); got != string(testCase.want) {
				t.Fatalf("PromptKind = %q, want %q", got, testCase.want)
			}
			// Labelling must never move the pane. A kind that arrived by way of
			// a reducer fold would show up here as a changed icon.
			if eff, visible := windowEffective(t, h, 42); eff.Icon != "key" || visible != "key" {
				t.Fatalf("effective = %+v visible %q, want key/key; the labeller changed state",
					eff, visible)
			}
		})
	}
}

// TestClaudePromptKindNotStampedOutsideTheBlockingClass is the icon gate. A
// still-working pane that happens to have prompt wording on screen — Claude
// leaves an answered prompt in scrollback for the rest of the session — must
// not acquire a reason, because there is no blocked row for one to explain.
func TestClaudePromptKindNotStampedOutsideTheBlockingClass(t *testing.T) {
	h := newTestHandler()
	base := time.Now()
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
			Profile: "work",
			Harness: "claude",
		}),
		getText: map[string]string{kitty.MatchWindowByID(42): claudePermissionScreen},
	}
	h.SetKittyClient(fake)
	claudeSeedActiveBolt(t, h, 42, "sess-1", base.Unix())

	h.ProbeClaudeScreenStop(base)
	h.ProbeClaudeScreenStop(base.Add(2 * time.Second))

	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
		t.Fatalf("effective = %+v, want the bolt preserved", eff)
	}
	if got := promptKind(t, h, 42); got != "" {
		t.Fatalf("PromptKind = %q, want empty on a ⚡ window", got)
	}
}

// TestClaudePromptKindClearedWhenPromptAnswered is the end-to-end clear. The
// clear itself is stampPromptKindLocked's and is already pinned for Codex; what
// is new here is that a kind the CLAUDE path installed goes through the same
// choke point, which is the whole reason session 3 made the clear an invariant
// of the icon rather than something a probe remembers to do.
func TestClaudePromptKindClearedWhenPromptAnswered(t *testing.T) {
	h, base := claudeBlockedWindow(t, claudeProceedPromptScreen)
	h.ProbeClaudeScreenStop(base.Add(2 * time.Second))
	if got := promptKind(t, h, 42); got != string(activity.ClaudePromptPermission) {
		t.Fatalf("PromptKind after the prompt appeared = %q, want %q",
			got, activity.ClaudePromptPermission)
	}

	// The user answers: Claude's Stop hook lands and the icon leaves the
	// blocking class on a path the probe never runs.
	idleSeedHarness(t, h, 42, "perm", "stop", base.Unix()+5)
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
		t.Fatalf("effective = %+v, want stop", eff)
	}
	if got := promptKind(t, h, 42); got != "" {
		t.Fatalf("PromptKind after the prompt was answered = %q, want empty", got)
	}
}

// TestClaudePromptKindAbsentUnderAFlatOverride is the compatibility story seen
// from the daemon: screenstate's own test proves a flat blocking_prompt still
// vetoes, and this proves the consequence the daemon has to be able to live
// with — a blocked Claude window with no reason at all. Session 3 already
// recorded that an empty reason is the common case (Copilot and the hook path
// produce one too), so this is the shape whatever renders the reason must read
// well against, not a failure.
func TestClaudePromptKindAbsentUnderAFlatOverride(t *testing.T) {
	h, base := claudeBlockedWindow(t, "Do you want to yodel?\n ❯ 1. Yes\n   2. No\n")

	h.ProbeClaudeScreenStop(base.Add(2 * time.Second))

	if got := promptKind(t, h, 42); got != "" {
		t.Fatalf("PromptKind = %q, want empty for copy no kind clause names", got)
	}
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "key" {
		t.Fatalf("effective = %+v, want key; an unlabelled prompt is still a prompt", eff)
	}
}

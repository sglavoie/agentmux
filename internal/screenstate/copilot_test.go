package screenstate

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/harness"
)

// The screens below are SYNTHETIC and every one of them is marked so, for the
// reason fixture_test.go documents at length: a hand-written screen can only
// confirm what its author already believed, and the bug this package exists to
// close survived a full suite of exactly such screens.
//
// That limitation is why nothing here asserts a CLASSIFICATION of Copilot's real
// UI. These screens are written against patterns_copilot.toml's provisional copy
// on purpose, and what they test is the adapter's STRUCTURE — that a matching
// working line raises the live-status veto, that a matching prompt raises the
// blocking veto and reports Blocked, that an empty composer clears the composer
// veto — none of which changes when the copy is replaced from real captures.
// The tests that need real copy are session 9's, and they need real files.
const (
	copilotSyntheticWorkingScreen = `● Reticulating… (4m 29s · ↓ 17.2k tokens)

╭──────────────────────────────────────────────────╮
│ ❯                                                │
╰──────────────────────────────────────────────────╯
`

	copilotSyntheticQuietScreen = `Here is the summary you asked for.

╭──────────────────────────────────────────────────╮
│ ❯                                                │
╰──────────────────────────────────────────────────╯
`

	copilotSyntheticPromptScreen = `Do you want to allow this tool to run?

  ❯ 1. Yes
    2. No

╭──────────────────────────────────────────────────╮
│ ❯                                                │
╰──────────────────────────────────────────────────╯
`

	copilotSyntheticQueuedInputScreen = `Here is the summary you asked for.

╭──────────────────────────────────────────────────╮
│ ❯ and now do the other thing                     │
╰──────────────────────────────────────────────────╯
`
)

func embeddedCopilotPatterns(t *testing.T) Patterns {
	t.Helper()
	set, ok := PatternsFor(harness.Copilot)
	if !ok {
		t.Fatal("no embedded pattern set for copilot")
	}
	if set.Source() != SourceEmbedded {
		t.Fatalf("Source() = %q, want %q; this test must run against the embedded set",
			set.Source(), SourceEmbedded)
	}
	return set
}

// TestCopilotAdapterIsRegistered is the registration half of track E's whole
// point: before this adapter existed, a Copilot row had no screen-based second
// line of defence at all because For() answered false.
func TestCopilotAdapterIsRegistered(t *testing.T) {
	adapter, ok := For(harness.Copilot)
	if !ok {
		t.Fatal("For(copilot) = false; the adapter must be registered")
	}
	if got := adapter.Harness(); got != harness.Copilot {
		t.Fatalf("Harness() = %q, want %q", got, harness.Copilot)
	}
	if _, ok := adapter.(copilotAdapter); !ok {
		t.Fatalf("For(copilot) returned %T, want copilotAdapter", adapter)
	}
	// The registered adapter must NOT carry a pinned set, or Load's override
	// would never reach it: the map is built at package init and read lock-free.
	if pinned := adapter.(copilotAdapter).Patterns; pinned.Harness() != "" {
		t.Fatalf("the registered adapter pinned a pattern set (%q); it must resolve per Classify",
			pinned.Harness())
	}
}

// TestCopilotAdapterNeverSettles pins the composition decision documented at the
// top of copilot.go as an executable invariant rather than a comment: for
// Copilot the transcript is the positive terminal evidence and the screen is
// refusal-only, so no screen — quiet, working, blocked, empty, or garbage — may
// ever produce a settleable observation.
//
// This is the test that makes the rest of the design safe. As long as it holds,
// wiring the screen adapter into internal/daemon/copilot_transcript_probe.go can
// only ever REMOVE a settle, never add one, and a Terminal Copilot observation
// can never miss internal/daemon/activity/policy.go's sample table and fall to
// the one-sample floor.
func TestCopilotAdapterNeverSettles(t *testing.T) {
	adapter := copilotAdapter{Patterns: embeddedCopilotPatterns(t)}
	screens := map[string]string{
		"working":      copilotSyntheticWorkingScreen,
		"quiet":        copilotSyntheticQuietScreen,
		"prompt":       copilotSyntheticPromptScreen,
		"queued input": copilotSyntheticQueuedInputScreen,
		"empty":        "",
		"unrecognized": "not a harness screen at all\njust some words\n",
	}
	for name, screen := range screens {
		observation := adapter.Classify(screen)
		if observation.SettleableTerminal() {
			t.Errorf("%s screen settled: %+v", name, observation)
		}
		if observation.State == Terminal {
			t.Errorf("%s screen classified Terminal; the Copilot screen is refusal-only", name)
		}
		if observation.Tier != TierNone {
			t.Errorf("%s screen graded %s; a refusal-only adapter has no tier to report",
				name, observation.Tier)
		}
		if observation.Fingerprint == 0 {
			t.Errorf("%s screen produced a zero fingerprint", name)
		}
	}
}

// TestCopilotFailsClosedOnUnrecognizedScreen is the fail-closed default stated
// directly. A screen the adapter cannot read must be Unknown — never Terminal
// and never Working — because absence of scary evidence read as evidence of a
// finished turn is the exact bug this package was created to close.
func TestCopilotFailsClosedOnUnrecognizedScreen(t *testing.T) {
	adapter := copilotAdapter{Patterns: embeddedCopilotPatterns(t)}
	for name, screen := range map[string]string{
		"empty":            "",
		"whitespace":       "   \n\n\t\n",
		"foreign harness":  "$ ls -la\ntotal 0\n",
		"prose about work": "I was working on the file for 5s and then interrupted it.\n",
	} {
		observation := adapter.Classify(screen)
		if observation.State != Unknown {
			t.Errorf("%s screen = %s, want unknown", name, observation.State)
		}
		// No composer on screen is not silence: it is its own named veto, and a
		// declined observation that named nothing would be indistinguishable
		// from a pane nobody looked at.
		if observation.Vetoes&VetoComposerBusy == 0 {
			t.Errorf("%s screen raised no composer veto: %s", name, observation.Vetoes)
		}
	}
}

// TestCopilotVetoesAreComputedFirstAndUnconditionally pins the Adapter
// contract's ordering the only way a caller can observe it: a screen carrying
// BOTH a live status line and a blocking prompt must report both vetoes, not
// the first one found, and must read Blocked rather than Working.
func TestCopilotVetoesAreComputedFirstAndUnconditionally(t *testing.T) {
	adapter := copilotAdapter{Patterns: embeddedCopilotPatterns(t)}

	working := adapter.Classify(copilotSyntheticWorkingScreen)
	if working.Vetoes&VetoLiveStatusLine == 0 {
		t.Fatalf("working screen raised no live-status veto: %s", working.Vetoes)
	}
	if working.State != Working {
		t.Errorf("working screen = %s, want working", working.State)
	}
	if working.Vetoes&VetoComposerBusy != 0 {
		t.Errorf("working screen's empty composer still raised composer_busy: %s", working.Vetoes)
	}
	if !strings.Contains(working.StatusLine, "Reticulating") {
		t.Errorf("StatusLine = %q, want the line the adapter keyed on", working.StatusLine)
	}

	both := adapter.Classify(copilotSyntheticWorkingScreen + copilotSyntheticPromptScreen)
	for _, want := range []Veto{VetoLiveStatusLine, VetoBlockingPrompt} {
		if both.Vetoes&want == 0 {
			t.Errorf("combined screen dropped %s; vetoes must be a SET, not a first hit", want)
		}
	}
	if both.State != Blocked {
		t.Errorf("combined screen = %s, want blocked; waiting on the user is the more "+
			"actionable reading", both.State)
	}

	queued := adapter.Classify(copilotSyntheticQueuedInputScreen)
	if queued.Vetoes != VetoComposerBusy {
		t.Errorf("queued-input screen vetoes = %s, want composer_busy alone", queued.Vetoes)
	}
	if queued.State != Unknown {
		t.Errorf("queued-input screen = %s, want unknown", queued.State)
	}
}

// TestCopilotChoiceLineAloneVetoes pins the half of the blocking-prompt veto
// that survives a reworded prompt. It is the reason choice_line is a required
// clause rather than a nicety: Copilot may reword "do you want to allow", but a
// selected numbered option is a shape, not a sentence.
func TestCopilotChoiceLineAloneVetoes(t *testing.T) {
	adapter := copilotAdapter{Patterns: embeddedCopilotPatterns(t)}
	screen := "Some question nobody wrote a phrase for\n\n  ❯ 1. Sure\n    2. No\n"
	observation := adapter.Classify(screen)
	if observation.Vetoes&VetoBlockingPrompt == 0 {
		t.Fatalf("a selected choice line raised no blocking veto: %s", observation.Vetoes)
	}
	if observation.State != Blocked {
		t.Errorf("State = %s, want blocked", observation.State)
	}
}

// TestCopilotRequirementsCoverEverySoleVetoSource re-derives the requirements
// list from the adapter's actual behaviour instead of trusting the comment above
// it. For each clause the adapter reads, it classifies a screen that fires that
// clause's veto against a set with the clause REMOVED: if the veto disappears,
// the clause was that veto's sole source and must therefore be required.
//
// This is the test the session's "getting this list right matters more than the
// patterns" note is asking for. A clause added later without a matching
// requirement fails here rather than shipping an override that can delete a
// refusal silently.
func TestCopilotRequirementsCoverEverySoleVetoSource(t *testing.T) {
	full := embeddedCopilotPatterns(t)
	required := make(map[string]bool, len(copilotRequirements))
	for _, name := range copilotRequirements.Names() {
		required[name] = true
	}

	// Each case is a screen that fires the clause's veto with the full set. The
	// composer clause is inverted — its veto fires on ABSENCE — so it is checked
	// separately below.
	cases := map[string]struct {
		screen string
		veto   Veto
	}{
		copilotClauseWorking: {
			screen: copilotSyntheticWorkingScreen,
			veto:   VetoLiveStatusLine,
		},
		copilotClauseBlockingPrompt: {
			screen: "Do you want to allow this tool to run?\n",
			veto:   VetoBlockingPrompt,
		},
		copilotClauseChoiceLine: {
			screen: "  ❯ 1. Sure\n",
			veto:   VetoBlockingPrompt,
		},
		copilotClauseInterruptHint: {
			screen: "esc to interrupt\n",
			veto:   VetoInterruptHint,
		},
	}
	for clause, tc := range cases {
		t.Run(clause, func(t *testing.T) {
			if (copilotAdapter{Patterns: full}).Classify(tc.screen).Vetoes&tc.veto == 0 {
				t.Fatalf("the full set does not raise %s for this screen; the case is stale", tc.veto)
			}
			without := (copilotAdapter{Patterns: full.Without(clause)}).Classify(tc.screen)
			soleSource := without.Vetoes&tc.veto == 0
			if soleSource && !required[clause] {
				t.Errorf("%q is the SOLE source of %s but is not in copilotRequirements; "+
					"an override could delete that refusal and still install", clause, tc.veto)
			}
			if !soleSource && required[clause] {
				t.Logf("%q is required but %s survives without it; harmless, but the "+
					"Reason string should say what is actually lost", clause, tc.veto)
			}
		})
	}

	// composer_line is the inverted one: losing it makes EVERY screen
	// composer_busy, so what it costs is not a veto but the ability to read a
	// pane as anything other than busy.
	t.Run(copilotClauseComposerLine, func(t *testing.T) {
		without := copilotAdapter{Patterns: full.Without(copilotClauseComposerLine)}
		if without.Classify(copilotSyntheticQuietScreen).Vetoes&VetoComposerBusy == 0 {
			t.Fatal("a set with no composer clause must read every screen as composer_busy")
		}
		if !required[copilotClauseComposerLine] {
			t.Error("composer_line must be required; without it no pane is ever readable")
		}
	})

	// And nothing required that the adapter does not read: a requirement for a
	// clause nobody consults refuses overrides for no safety gain.
	read := map[string]bool{
		copilotClauseComposerLine:   true,
		copilotClauseChoiceLine:     true,
		copilotClauseWorking:        true,
		copilotClauseInterruptHint:  true,
		copilotClauseBlockingPrompt: true,
	}
	for name := range required {
		if !read[name] {
			t.Errorf("copilotRequirements demands %q, which the adapter never reads", name)
		}
	}
	for _, requirement := range copilotRequirements {
		if strings.TrimSpace(requirement.Reason) == "" {
			t.Errorf("%q has no Reason; the doctor finding it produces would not be actionable",
				requirement.Name)
		}
	}
}

// TestCopilotIncompleteSetStillCannotSettle is the pin behind Classify's
// deliberate absence of a copilotRequirements.Satisfied gate. Claude re-checks
// its requirements inside Classify so an incomplete set reaching the adapter
// through a public seam cannot settle a pane; there is no settle here to guard,
// which is only true while Terminal stays unreachable.
//
// If a Terminal branch is ever added to the Copilot adapter, this fails and the
// Satisfied gate becomes load-bearing — which is the point of asserting the
// equivalence rather than writing it in a comment.
func TestCopilotIncompleteSetStillCannotSettle(t *testing.T) {
	full := embeddedCopilotPatterns(t)
	stripped := full.Without(copilotRequirements.Names()...)
	if copilotRequirements.Satisfied(stripped) {
		t.Fatal("the stripped set still satisfies the requirements; the case is stale")
	}
	adapter := copilotAdapter{Patterns: stripped}
	for name, screen := range map[string]string{
		"quiet":   copilotSyntheticQuietScreen,
		"working": copilotSyntheticWorkingScreen,
		"prompt":  copilotSyntheticPromptScreen,
	} {
		observation := adapter.Classify(screen)
		if observation.SettleableTerminal() {
			t.Errorf("%s screen settled against an incomplete set: %+v", name, observation)
		}
		if observation.State != Unknown {
			t.Errorf("%s screen = %s against an incomplete set, want unknown", name, observation.State)
		}
	}
}

// TestCopilotPatternsAreMarkedProvisional pins the marker session 9 drops. The
// copy in patterns_copilot.toml has never been checked against a live Copilot
// pane, and a reader who does not know that would reasonably trust it — so the
// file says so, and this fails if the banner is removed while the file is still
// the one this session wrote.
//
// It deliberately asserts only that the word is present, not where: the whole
// file is rewritten from captures in session 9, and a test that pinned the
// banner's exact position would fail for the right reason at the wrong moment.
func TestCopilotPatternsAreMarkedProvisional(t *testing.T) {
	body := embeddedPatternFile(t, harness.Copilot)
	if !strings.Contains(body, "PROVISIONAL") {
		t.Error("patterns_copilot.toml no longer marks itself provisional; if the copy is now " +
			"from real captures, delete this test in the same change")
	}
}

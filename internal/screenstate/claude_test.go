package screenstate

import "testing"

// claudeObservation classifies through the registry rather than the concrete
// type, so a broken For() registration fails these tests too.
func claudeObservation(t *testing.T, screen string) Observation {
	t.Helper()

	adapter, ok := For("claude")
	if !ok {
		t.Fatal(`For("claude") returned no adapter`)
	}
	if got := adapter.Harness(); got != "claude" {
		t.Fatalf("Harness() = %q, want %q", got, "claude")
	}
	return adapter.Classify(screen)
}

// TestClassifyClaudeIdleScreen moved here verbatim in intent from
// internal/daemon/claude_prompt_test.go when the classifier moved. Every case
// that had a real session-2 capture behind it now uses that capture instead of
// the hand-written screen it shipped with; the remainder are shapes no capture
// exercises (a bracketed interruption from an older build, a screen scrolled
// past its composer) and stay synthetic, marked as such.
func TestClassifyClaudeIdleScreen(t *testing.T) {
	cases := []struct {
		name    string
		screen  string
		want    bool
		state   State
		tier    Tier
		vetoes  Veto
		fixture bool
	}{
		{
			name: "completed turn with empty composer", fixture: true,
			screen: "claude_idle_completed.txt",
			want:   true, state: Terminal, tier: TierTerminalShape,
		},
		{
			name: "session start with a bare composer and no status line", fixture: true,
			screen: "claude_idle_bare_composer.txt",
			want:   true, state: Terminal, tier: TierQuiescent,
		},
		{
			name: "interrupted turn with empty composer", fixture: true,
			screen: "claude_interrupted.txt",
			want:   true, state: Terminal, tier: TierTerminalExplicit,
		},
		{
			name: "live thinking turn with an empty composer", fixture: true,
			screen: "claude_working_pouncing_t0.txt",
			want:   false, state: Working, tier: TierNone, vetoes: VetoLiveStatusLine,
		},
		{
			name: "live turn whose parentheses hold a past-tense fragment", fixture: true,
			screen: "claude_working_reticulating.txt",
			want:   false, state: Working, tier: TierNone, vetoes: VetoLiveStatusLine,
		},
		{
			name: "queued input in composer", fixture: true,
			screen: "claude_queued_input.txt",
			want:   false, state: Working, tier: TierNone,
			vetoes: VetoLiveStatusLine | VetoComposerBusy,
		},
		{
			name: "permission prompt is blocking", fixture: true,
			screen: "claude_permission_prompt.txt",
			want:   false, state: Blocked, tier: TierNone,
			vetoes: VetoBlockingPrompt | VetoComposerBusy,
		},
		{
			name: "context limit marker", fixture: true,
			screen: "claude_context_limit.txt",
			want:   false, state: Blocked, tier: TierNone, vetoes: VetoContextLimit,
		},
		{
			name:   "legacy esc-to-interrupt hint still vetoes",
			screen: claudeLegacyWorkingScreen,
			want:   false, state: Working, tier: TierNone,
			vetoes: VetoLiveStatusLine | VetoInterruptHint,
		},
		{
			name:   "background task running",
			screen: claudeBackgroundTaskScreen,
			want:   false, state: Working, tier: TierNone, vetoes: VetoBackgroundTask,
		},
		{
			name:   "no composer at all",
			screen: claudeNoComposerScreen,
			want:   false, state: Unknown, tier: TierNone, vetoes: VetoComposerBusy,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			screen := tc.screen
			if tc.fixture {
				screen = readScreenFixture(t, tc.screen)
			}
			got := claudeObservation(t, screen)
			if got.SettleableTerminal() != tc.want {
				t.Fatalf("SettleableTerminal() = %v, want %v (state %s, vetoes %06b)",
					got.SettleableTerminal(), tc.want, got.State, got.Vetoes)
			}
			if got.State != tc.state {
				t.Errorf("State = %s, want %s", got.State, tc.state)
			}
			if got.Tier != tc.tier {
				t.Errorf("Tier = %s, want %s", got.Tier, tc.tier)
			}
			if got.Vetoes != tc.vetoes {
				t.Errorf("Vetoes = %06b, want %06b", got.Vetoes, tc.vetoes)
			}
			if got.Fingerprint == 0 {
				t.Error("Fingerprint = 0, want the never-zero screen hash")
			}
		})
	}
}

// TestClassifyClaudeInterruptedScreen pins the tier that names WHY a screen
// looks finished. The old daemon bool is now "settleable AND explicitly
// marked", so this test asserts the tier directly.
func TestClassifyClaudeInterruptedScreen(t *testing.T) {
	cases := []struct {
		name    string
		screen  string
		want    bool
		fixture bool
	}{
		{name: "interrupted marker with composer", screen: "claude_interrupted.txt", fixture: true, want: true},
		{name: "request interrupted bracket form", screen: claudeRequestInterruptedScreen, want: true},
		{name: "completed turn is not an interruption", screen: "claude_idle_completed.txt", fixture: true},
		{name: "interrupted then resumed working", screen: claudeInterruptedThenWorkingScreen},
		{name: "interrupted without composer", screen: claudeNoComposerScreen},
		{name: "transcript prose mentioning interrupted", screen: claudeInterruptedProseScreen},
		{name: "permission prompt is not an interruption", screen: "claude_permission_prompt.txt", fixture: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			screen := tc.screen
			if tc.fixture {
				screen = readScreenFixture(t, tc.screen)
			}
			got := claudeObservation(t, screen)
			explicit := got.SettleableTerminal() && got.Tier == TierTerminalExplicit
			if explicit != tc.want {
				t.Fatalf("settleable explicit interruption = %v, want %v (state %s, tier %s, vetoes %06b)",
					explicit, tc.want, got.State, got.Tier, got.Vetoes)
			}
		})
	}
}

// TestClaudeCompletedStatusLineRejectsWorkingDurations is the load-bearing
// invariant in isolation. Claude's live status line embeds a past-tense
// duration fragment inside its parentheses, so a finished-line regex without
// the end anchor reads a working pane as finished — and, because vetoes are
// computed first, even a regex that DID match would not be consulted. Both
// halves are asserted, because either alone would let the bug back in if the
// other were later relaxed.
func TestClaudeCompletedStatusLineRejectsWorkingDurations(t *testing.T) {
	rejected := []struct {
		name string
		line string
	}{
		{"working line whose parentheses hold a past-tense duration", "● Reticulating… (4m 29s · ↓ 17.2k tokens · thought for 5s)"},
		{"finished shape with trailing prose", "Waited for 3s (see above)"},
		{"transcript prose narrating an interruption", "The staging deploy was interrupted for the cluster last week."},
		{"footer hint", "  ⏵⏵ bypass permissions on (shift+tab to cycle) · ← for agents"},
		{"token counter", "                                    113620 tokens"},
	}
	for _, tc := range rejected {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			if claudeCompletedStatusLineRE.MatchString(tc.line) {
				t.Fatalf("claudeCompletedStatusLineRE matched %q, want no match", tc.line)
			}
		})
	}

	accepted := []string{"✻ Baked for 16m 32s", "✻ Cooked for 17m 6s", "✻ Sautéed for 8s"}
	for _, line := range accepted {
		t.Run("matches "+line, func(t *testing.T) {
			if !claudeCompletedStatusLineRE.MatchString(line) {
				t.Fatalf("claudeCompletedStatusLineRE did not match %q", line)
			}
		})
	}

	// The completed captures the accepted lines came from must actually observe
	// as terminal, so the regex above is not green in isolation while the
	// adapter disagrees.
	for _, name := range []string{"claude_idle_completed.txt", "claude_idle_completed_trailing_space.txt"} {
		t.Run("fixture "+name, func(t *testing.T) {
			got := claudeObservation(t, readScreenFixture(t, name))
			if got.Tier != TierTerminalShape {
				t.Fatalf("Tier = %s, want %s (state %s)", got.Tier, TierTerminalShape, got.State)
			}
		})
	}

	// The veto half: even on the reticulating capture, whose completed-shape
	// regex is deliberately not matched, the working veto is what decides.
	working := claudeObservation(t, readScreenFixture(t, "claude_working_reticulating.txt"))
	if working.Vetoes&VetoLiveStatusLine == 0 {
		t.Fatalf("Vetoes = %06b, want VetoLiveStatusLine set on a live working capture", working.Vetoes)
	}
}

// TestClaudeWorkingStatusLineCorpus pins the working veto against the exact
// corpus the design was verified over, including the legacy shape older builds
// still render and every non-status line that shares its neighbourhood.
func TestClaudeWorkingStatusLineCorpus(t *testing.T) {
	matches := []string{
		"✽ Pouncing…     (5m 40s · ↓ 28.5k tokens)",
		"· Pouncing…     (5m 43s · ↓ 28.8k tokens)",
		"● Reticulating… (4m 29s · ↓ 17.2k tokens · thought for 5s)",
		"✳ Pouncing…     (3m 53s · ↓ 11.7k tokens · thinking with xhigh effort)",
		"✳ Thinking… (12s · ↑ 2.1k tokens · esc to interrupt)",
	}
	for _, line := range matches {
		if !claudeWorkingStatusLineRE.MatchString(line) {
			t.Errorf("claudeWorkingStatusLineRE did not match %q", line)
		}
	}

	rejects := []string{
		"✻ Baked for 16m 32s",
		"✻ Cooked for 17m 6s",
		"Waited for 3s (see above)",
		"⎿ Interrupted by user",
		"The staging deploy was interrupted for the cluster last week.",
		"                                    113620 tokens",
		"  27.0 max 5h ✗ 7d ✗ | team 5h 30/26 2:29pm 7d 56/76 jun10|2am snt 4 $0",
		"  ⏵⏵ bypass permissions on (shift+tab to cycle) · ← for agents",
		"❯ ",
		"⏺ Bash(sleep 30)",
	}
	for _, line := range rejects {
		if claudeWorkingStatusLineRE.MatchString(line) {
			t.Errorf("claudeWorkingStatusLineRE matched %q, want no match", line)
		}
	}
}

// TestClaudeLatestStatusLineWins pins the ordering rule. Claude leaves the
// previous turn's markers scrolled above the current one, so only the LAST
// recognized status line may decide — an "anywhere on screen" test lets a
// marker from minutes ago outvote the live line directly below it.
func TestClaudeLatestStatusLineWins(t *testing.T) {
	cases := []struct {
		name   string
		screen string
		state  State
		tier   Tier
	}{
		{
			name:   "working after completed resolves working",
			screen: claudeCompletedThenWorkingScreen,
			state:  Working, tier: TierNone,
		},
		{
			name:   "completed after working resolves terminal",
			screen: claudeWorkingThenCompletedScreen,
			state:  Terminal, tier: TierTerminalShape,
		},
		{
			name:   "interrupted then working resolves working",
			screen: claudeInterruptedThenWorkingScreen,
			state:  Working, tier: TierNone,
		},
		{
			name:   "working then interrupted resolves an explicit terminal",
			screen: claudeWorkingThenInterruptedScreen,
			state:  Terminal, tier: TierTerminalExplicit,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := claudeObservation(t, tc.screen)
			if got.State != tc.state || got.Tier != tc.tier {
				t.Fatalf("State/Tier = %s/%s, want %s/%s (vetoes %06b, status %q)",
					got.State, got.Tier, tc.state, tc.tier, got.Vetoes, got.StatusLine)
			}
		})
	}

	// The live capture proves the rule on real bytes rather than only on
	// hand-written ones: claude_working_pouncing_t0.txt carries two of Claude's
	// own interruption markers from earlier turns, scrolled above a status line
	// that is live right now.
	got := claudeObservation(t, readScreenFixture(t, "claude_working_pouncing_t0.txt"))
	if got.State != Working {
		t.Fatalf("State = %s on a capture holding stale interruption markers, want %s", got.State, Working)
	}
}

// TestClaudeNarrowPaneIsLayerBOnly records a KNOWN blind spot rather than a
// property to be proud of. Below roughly 20 columns Claude truncates its status
// line to a bare gerund with no parenthesized timer, so the working veto sees
// nothing and a genuinely working pane observes as Terminal. That is why Layer A
// must never ship without the fingerprint gate: the narrow pair still differs
// between ticks, so stabilization refuses to settle it. If this test ever starts
// failing because the adapter learned to read a narrow pane, delete it — do not
// weaken the veto to keep it green.
func TestClaudeNarrowPaneIsLayerBOnly(t *testing.T) {
	t0 := readScreenFixture(t, "claude_working_narrow_t0.txt")
	t1 := readScreenFixture(t, "claude_working_narrow_t1.txt")

	if got := claudeObservation(t, t0); got.State != Terminal {
		t.Logf("narrow-pane blind spot closed: State = %s", got.State)
		return
	}
	if Fingerprint(t0) == Fingerprint(t1) {
		t.Fatal("narrow working frames share a fingerprint: Layer A is blind here and Layer B would be too")
	}
}

// TestClaudeStatusLineIsBounded pins the diagnostics-only contract on
// Observation.StatusLine.
func TestClaudeStatusLineIsBounded(t *testing.T) {
	got := claudeObservation(t, readScreenFixture(t, "claude_idle_completed.txt"))
	if got.StatusLine != "✻ Sautéed for 8s" {
		t.Fatalf("StatusLine = %q, want the completed line", got.StatusLine)
	}

	long := "✻ Baked for 1s " + string(make([]rune, 400)) + "\n❯ \n"
	if n := len([]rune(claudeObservation(t, long).StatusLine)); n > maxStatusLineRunes {
		t.Fatalf("StatusLine = %d runes, want at most %d", n, maxStatusLineRunes)
	}
}

// TestScreenstateForUnknownHarness pins the deliberate difference from
// harness.Lookup: no adapter is returned for a harness this package cannot
// classify, because classifying a Copilot pane with Claude's UI copy would
// produce a confident wrong answer.
func TestScreenstateForUnknownHarness(t *testing.T) {
	for _, name := range []string{"  Claude  ", "CLAUDE", "  Codex  ", "CODEX"} {
		if _, ok := For(name); !ok {
			t.Errorf("For(%q) = false, want a registered adapter", name)
		}
	}
	for _, name := range []string{"", "copilot", "unknown"} {
		if _, ok := For(name); ok {
			t.Errorf("For(%q) returned an adapter, want none", name)
		}
	}
}

// TestObservationSettleableTerminal pins the conjunction directly, so an
// adapter that ever set Terminal alongside a veto cannot settle.
func TestObservationSettleableTerminal(t *testing.T) {
	if !(Observation{State: Terminal}).SettleableTerminal() {
		t.Error("a veto-free Terminal observation must be settleable")
	}
	if (Observation{State: Terminal, Vetoes: VetoComposerBusy}).SettleableTerminal() {
		t.Error("a vetoed observation must never be settleable")
	}
	if (Observation{}).SettleableTerminal() {
		t.Error("the zero Observation must never be settleable")
	}
}

// The screens below have no live capture behind them and are marked synthetic
// for exactly the reason fixture_test.go documents: a hand-written screen can
// only confirm what its author already believed. Each one exists because the
// shape it holds is either from an older Claude build (the bracketed
// interruption, the esc-to-interrupt hint) or is an ordering permutation that
// would need a turn interrupted at a one-second boundary to capture live.

const claudeLegacyWorkingScreen = `✳ Thinking… (12s · ↑ 2.1k tokens · esc to interrupt)

╭──────────────────────────────────────────────────╮
│ ❯                                                  │
╰──────────────────────────────────────────────────╯
`

const claudeBackgroundTaskScreen = `Running go test in the background (ctrl+b to manage background tasks)

╭──────────────────────────────────────────────────╮
│ ❯                                                  │
╰──────────────────────────────────────────────────╯
`

const claudeNoComposerScreen = `⎿ Interrupted by user

Some trailing output with no framed composer at all.
`

const claudeRequestInterruptedScreen = `[Request interrupted by user]

╭──────────────────────────────────────────────────╮
│ ❯                                                  │
╰──────────────────────────────────────────────────╯
`

const claudeInterruptedThenWorkingScreen = `⎿ Interrupted by user

✳ Thinking… (3s · esc to interrupt)

╭──────────────────────────────────────────────────╮
│ ❯                                                  │
╰──────────────────────────────────────────────────╯
`

const claudeInterruptedProseScreen = `✻ Cooked for 1m 4s

The staging deploy was interrupted for the cluster last week.

╭──────────────────────────────────────────────────╮
│ ❯                                                  │
╰──────────────────────────────────────────────────╯
`

const claudeCompletedThenWorkingScreen = `✻ Baked for 16m 32s

✽ Pouncing… (5m 40s · ↓ 28.5k tokens)

╭──────────────────────────────────────────────────╮
│ ❯                                                  │
╰──────────────────────────────────────────────────╯
`

const claudeWorkingThenCompletedScreen = `✽ Pouncing… (5m 40s · ↓ 28.5k tokens)

✻ Baked for 16m 32s

╭──────────────────────────────────────────────────╮
│ ❯                                                  │
╰──────────────────────────────────────────────────╯
`

const claudeWorkingThenInterruptedScreen = `✽ Pouncing… (5m 40s · ↓ 28.5k tokens)

⎿ Interrupted · What should Claude do instead?

╭──────────────────────────────────────────────────╮
│ ❯                                                  │
╰──────────────────────────────────────────────────╯
`

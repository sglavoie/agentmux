package screenstate

import (
	"strings"
	"testing"
)

// codexObservation classifies through the registry rather than the concrete
// type, so a broken For() registration fails these tests too.
func codexObservation(t *testing.T, screen string) Observation {
	t.Helper()

	adapter, ok := For("codex")
	if !ok {
		t.Fatal(`For("codex") returned no adapter`)
	}
	if got := adapter.Harness(); got != "codex" {
		t.Fatalf("Harness() = %q, want %q", got, "codex")
	}
	return adapter.Classify(screen)
}

// TestCodexLiveElapsedStatusLine pins the word-independent working veto against
// the corpus the design was verified over.
//
// The rejection half is the load-bearing one: this pattern runs on the same
// lines as codexWorkedForLineRE, so a pattern that also matched "Worked for 1m
// 30s" would veto every completed turn and strand Codex panes on ⚡ forever. The
// absent parentheses are what separate them.
func TestCodexLiveElapsedStatusLine(t *testing.T) {
	matches := []string{
		"• Working (5s • Esc to interrupt)",
		"⠋ Working (12s · Esc to interrupt)",
		"• Cogitating (3s)",
		"◐ Ruminating   ( 1h 4m )",
	}
	for _, line := range matches {
		if !codexLiveElapsedStatusLineRE.MatchString(line) {
			t.Errorf("codexLiveElapsedStatusLineRE did not match %q", line)
		}
	}

	rejects := []string{
		"• Worked for 1m 30s",
		"• Worked for 3s",
		"⏹ Interrupted by user",
		"› Use /skills to list available skills",
		"  gpt-5.5 xhigh · ~/1_dev_projects/sglavoie_agentmux · Context 12% used",
		"  └ (no output)",
		"• The migration worked for the staging cluster last week.",
	}
	for _, line := range rejects {
		if codexLiveElapsedStatusLineRE.MatchString(line) {
			t.Errorf("codexLiveElapsedStatusLineRE matched %q, want no match", line)
		}
	}
}

// TestCodexStatusLineRegexesOverFixtures runs the same proof over the session-2
// captures rather than over hand-written lines, so the "Worked for" non-match is
// asserted against real bytes from a real pane. The captures are scanned line by
// line because every one of these patterns is line-anchored.
func TestCodexStatusLineRegexesOverFixtures(t *testing.T) {
	liveWork := map[string]bool{
		"codex_working.txt":                   true,
		"codex_idle_completed_t0.txt":         false,
		"codex_idle_completed_t1.txt":         false,
		"codex_interrupted.txt":               false,
		"codex_cleared.txt":                   false,
		"codex_interrupted_no_background.txt": false,
	}
	for name, want := range liveWork {
		t.Run(name, func(t *testing.T) {
			var live, worked []string
			for _, line := range strings.Split(readScreenFixture(t, name), "\n") {
				if codexLineLooksLiveWork(line) {
					live = append(live, line)
				}
				if codexWorkedForLineRE.MatchString(line) {
					worked = append(worked, line)
				}
			}
			if got := len(live) > 0; got != want {
				t.Fatalf("codexLineLooksLiveWork over %s = %v, want %v (lines %q)", name, got, want, live)
			}
			// codex-cli 0.149.1 emits no "Worked for N" line at all (session 2),
			// so no capture may produce one. If this ever fails, the capture is
			// from a build that does — and the completed classifier finally has
			// live evidence behind it.
			for _, line := range worked {
				if codexLiveElapsedStatusLineRE.MatchString(line) {
					t.Fatalf("codexLiveElapsedStatusLineRE matched a completed line %q", line)
				}
			}
		})
	}
}

// TestCodexRenamedSpinnerStillVetoes is the reason the two new patterns exist.
//
// Both screens below are a finished "Worked for" line, a LIVE status line whose
// spinner word is not "Working", and an empty composer after both. Under the
// word-dependent veto alone each one settles a synthetic stop on a pane that is
// visibly still running — the exact class of bug this plan exists to close, one
// harness release away. Each screen isolates one of the two replacements.
func TestCodexRenamedSpinnerStillVetoes(t *testing.T) {
	cases := []struct {
		name   string
		screen string
	}{
		{name: "renamed spinner still ticks a timer", screen: codexRenamedSpinnerTimerScreen},
		{name: "renamed spinner still names an interrupt key", screen: codexRenamedSpinnerHintScreen},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if ClassifyCodexCompletedTurnScreen(tc.screen) {
				t.Fatal("ClassifyCodexCompletedTurnScreen() = true on a live screen")
			}
			if got := codexObservation(t, tc.screen); got.State != Working {
				t.Fatalf("State = %s, want %s (vetoes %06b, status %q)",
					got.State, Working, got.Vetoes, got.StatusLine)
			}
		})
	}
}

// TestCodexComposerMustFollowTheStatusLine pins the positional requirement in
// isolation. A composer rendered ABOVE a still-current terminal marker is not
// evidence the marker's turn ended — it is the previous turn's composer scrolled
// up — so "an empty composer somewhere on screen" is the wrong test. Both
// classifiers share one implementation, so both are asserted.
func TestCodexComposerMustFollowTheStatusLine(t *testing.T) {
	if ClassifyCodexCompletedTurnScreen(codexComposerBeforeWorkedForScreen) {
		t.Error("ClassifyCodexCompletedTurnScreen() = true with the composer above the marker")
	}
	if ClassifyCodexInterruptedTurnScreen(codexComposerBeforeInterruptedScreen) {
		t.Error("ClassifyCodexInterruptedTurnScreen() = true with the composer above the marker")
	}
	// The same bytes with the composer moved below the marker DO settle, so the
	// assertions above fail for the position and not for some unrelated reason.
	if !ClassifyCodexCompletedTurnScreen(codexCompletedTurnScreen) {
		t.Error("ClassifyCodexCompletedTurnScreen() = false on the completed capture")
	}
	if !ClassifyCodexInterruptedTurnScreen(codexInterruptedTurnScreen) {
		t.Error("ClassifyCodexInterruptedTurnScreen() = false on the interrupted capture")
	}
}

// TestCodexAdapterObservations pins the adapter's State/Tier/Veto reading.
//
// The tier column is the part session 7 keys its policy table on: BOTH Codex
// terminal markers are TierTerminalExplicit, and no Codex screen may ever be
// settleable at TierNone — a tier with no policy row falls to Go's zero value,
// which the probe floors to a single sample.
func TestCodexAdapterObservations(t *testing.T) {
	cases := []struct {
		name    string
		screen  string
		fixture bool
		want    bool
		state   State
		tier    Tier
		vetoes  Veto
	}{
		{
			name: "completed turn with composer", screen: codexCompletedTurnScreen,
			want: true, state: Terminal, tier: TierTerminalExplicit,
		},
		{
			name: "interrupted turn with composer", screen: codexInterruptedTurnScreen,
			want: true, state: Terminal, tier: TierTerminalExplicit,
		},
		{
			name: "installed interruption with dim placeholder", screen: codexInstalledInterruptedTurnScreen,
			want: true, state: Terminal, tier: TierTerminalExplicit,
		},
		{
			name: "live working turn", screen: codexWorkingTurnScreen,
			state: Working, vetoes: VetoLiveStatusLine | VetoInterruptHint,
		},
		{
			name: "visible prompt outranks everything", screen: codexCompletedThenPromptScreen,
			state: Blocked, vetoes: VetoBlockingPrompt | VetoComposerBusy,
		},
		{
			name: "terminal marker with no composer at all", screen: codexInterruptedNoComposerScreen,
			state: Unknown, vetoes: VetoComposerBusy,
		},
		{
			// Codex's counterpart to Claude's quiescent tier — except Codex never
			// settles it, because Codex always prints a marker when a turn ends.
			name: "bare composer with no marker", screen: codexClearedComposerOnlyScreen,
			state: Unknown,
		},
		{
			// The composer IS recognized on a 0.149.1 screen now, so this working
			// capture no longer carries VetoComposerBusy — and that is the point:
			// the two live-work vetoes are what hold it, exactly as they must once
			// the composer stops doing the job by accident. The capture is also
			// running a background terminal, whose footer Codex appends to the
			// "Working (…)" line, so the background veto rides along.
			name: "live capture", screen: "codex_working.txt", fixture: true,
			state: Working, vetoes: VetoLiveStatusLine | VetoInterruptHint | VetoBackgroundTask,
		},
		{
			// The footer reads "Press enter to confirm or esc to cancel", so both
			// word-independent working signals fire on a screen that is blocked
			// rather than working: the hint veto directly, and the live-status-line
			// veto because that same footer is the last line codexLatestStatusLine
			// recognizes. Blocked outranks Working and neither settles, which is
			// exactly why the overlap costs nothing.
			name: "command approval capture", screen: "codex_prompt_command_approval.txt", fixture: true,
			state: Blocked, vetoes: VetoBlockingPrompt | VetoInterruptHint | VetoLiveStatusLine | VetoComposerBusy,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			screen := tc.screen
			if tc.fixture {
				screen = readScreenFixture(t, tc.screen)
			}
			got := codexObservation(t, screen)
			if got.SettleableTerminal() != tc.want {
				t.Fatalf("SettleableTerminal() = %v, want %v (state %s, vetoes %06b)",
					got.SettleableTerminal(), tc.want, got.State, got.Vetoes)
			}
			if got.SettleableTerminal() && got.Tier == TierNone {
				t.Fatal("Tier = none on a settleable screen: the probe would floor it to one sample")
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

// TestCodexTiersReachableToday records, as an executable statement rather than a
// comment, that TierTerminalShape and TierQuiescent are unreachable for Codex.
// Session 7 carries a policy row for all three anyway, for forward-compat; if
// this test ever fails, a new terminal shape arrived and that row stopped being
// a duplicate of the explicit one.
func TestCodexTiersReachableToday(t *testing.T) {
	screens := []string{
		codexCompletedTurnScreen, codexInterruptedTurnScreen, codexConversationInterruptedScreen,
		codexInstalledInterruptedTurnScreen, codexWorkingTurnScreen, codexWorkedThenWorkingScreen,
		codexInterruptedThenWorkingScreen, codexWorkedForNoComposerScreen, codexInterruptedNoComposerScreen,
		codexWorkedForProseScreen, codexInterruptedProseScreen, codexClearedComposerOnlyScreen,
		codexCompletedThenPromptScreen, codexInterruptedThenPromptScreen, codexReportedClearedScreen,
		codexClearedScreen, codexNonzeroContextScreen, codexEditApprovalScreen,
		codexRenamedSpinnerTimerScreen, codexRenamedSpinnerHintScreen,
		codexFramedCompletedScreen, codexUnframedCompletedScreen,
		codexHalfFramedCompletedScreen, codexCompletedProseScreen,
		codexCompletedWithBackgroundTerminalScreen,
	}
	for _, name := range []string{
		"codex_working.txt", "codex_idle_completed_t0.txt", "codex_idle_completed_t1.txt",
		"codex_interrupted.txt", "codex_interrupted_no_background.txt",
		"codex_cleared.txt", "codex_prompt_command_approval.txt",
	} {
		screens = append(screens, readScreenFixture(t, name))
	}

	for i, screen := range screens {
		got := codexObservation(t, screen)
		switch got.Tier {
		case TierNone, TierTerminalExplicit:
		default:
			t.Errorf("screen %d observed Tier %s; only none and terminal_explicit are reachable for Codex today", i, got.Tier)
		}
		if got.State == Terminal && got.Tier != TierTerminalExplicit {
			t.Errorf("screen %d is Terminal at Tier %s, want terminal_explicit", i, got.Tier)
		}
	}
}

// TestCodexComposerPlaceholderIsNarrow pins the accepted residual brittleness
// and, more importantly, the narrowness that pays for it.
//
// codexComposerLineRE hardcodes the dim placeholder strings because Kitty's
// get-text drops the styling that would identify them structurally. A build
// shipping an unknown placeholder makes the composer unmatchable and withholds
// settling — a stuck ⚡ and never a false 🛑, which is the direction this package
// fails in by design.
//
// The rejects are the load-bearing half. Every one of them is a real chevron
// line from a committed 0.149.1 capture; accepting arbitrary text after the
// chevron would let an old transcript prompt satisfy the positional composer
// scan and settle a pane mid-turn.
func TestCodexComposerPlaceholderIsNarrow(t *testing.T) {
	accepted := []string{
		"› ",
		"› Use /skills to list available skills",
		"› Find and fix a bug in @filename",
		"› Ask Codex to do anything",
	}
	for _, line := range accepted {
		if !codexComposerLineRE.MatchString(line) {
			t.Errorf("codexComposerLineRE did not match %q", line)
		}
	}

	rejected := []string{
		"› Run the shell command: sleep 8",
		"› Reply with just the word ok and do nothing else.",
		"› Write a detailed 3000-word essay about the history of the number seven.",
		"› Ask Codex to do anything you like",
		"› 1. Yes, proceed (y)",
	}
	for _, line := range rejected {
		if codexComposerLineRE.MatchString(line) {
			t.Errorf("codexComposerLineRE matched arbitrary chevron text %q; the end anchor is gone", line)
		}
	}
}

// TestCodexCurrentScreenContract is the whole point of this repair: the screens
// the INSTALLED Codex actually renders must drive the terminal and clear
// recovery paths, not merely be committed as evidence that they do not.
//
// Every screen below is a byte-for-byte capture of a live codex-cli 0.149.1
// pane. Before this change all of them carried VetoComposerBusy — the build's
// "Ask Codex to do anything" placeholder was unknown — so the stop fallback and
// the clear probe were unreachable on the only Codex version anyone here runs,
// while docs/daemon.md and docs/operations.md described both as available.
func TestCodexCurrentScreenContract(t *testing.T) {
	cases := []struct {
		name    string
		fixture string
		want    bool
		state   State
		tier    Tier
		vetoes  Veto
	}{
		{
			// The framed "• Completed." marker, with the composer below it.
			name: "framed completed marker settles", fixture: "codex_idle_completed_t0.txt",
			want: true, state: Terminal, tier: TierTerminalExplicit,
		},
		{
			name: "the same pane 3s later still settles", fixture: "codex_idle_completed_t1.txt",
			want: true, state: Terminal, tier: TierTerminalExplicit,
		},
		{
			// Interruption recovery on a current pane with nothing left running.
			name:    "current interruption with no background terminal settles",
			fixture: "codex_interrupted_no_background.txt",
			want:    true, state: Terminal, tier: TierTerminalExplicit,
		},
		{
			// A live turn is still refused, and now on the strength of the
			// live-work vetoes alone rather than on an unreadable composer. This
			// capture also happens to be running a background terminal, and Codex
			// appends that footer to the "Working (…)" line itself, so the third
			// veto is expected here too.
			name: "live turn is still refused", fixture: "codex_working.txt",
			state: Working, vetoes: VetoLiveStatusLine | VetoInterruptHint | VetoBackgroundTask,
		},
		{
			// The flip repair group codex-background-task-veto owned. This capture
			// is an interrupted turn — recognized marker, no live status line,
			// accepted composer, every settling gate satisfied — that nonetheless
			// reports "1 background terminal running". It settled until the footer
			// got a veto of its own; contrast the row above, which is the same
			// interruption with nothing left running.
			name:    "current interruption with a background terminal is refused",
			fixture: "codex_interrupted.txt",
			state:   Working, vetoes: VetoBackgroundTask,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := codexObservation(t, readScreenFixture(t, tc.fixture))
			if got.SettleableTerminal() != tc.want {
				t.Fatalf("SettleableTerminal() = %v, want %v (state %s, vetoes %s)",
					got.SettleableTerminal(), tc.want, got.State, got.Vetoes)
			}
			if got.State != tc.state {
				t.Errorf("State = %s, want %s", got.State, tc.state)
			}
			if got.Tier != tc.tier {
				t.Errorf("Tier = %s, want %s", got.Tier, tc.tier)
			}
			if got.Vetoes != tc.vetoes {
				t.Errorf("Vetoes = %s, want %s", got.Vetoes, tc.vetoes)
			}
		})
	}

	// Clear recovery on the current build. codex_cleared.txt is a real post-
	// startup 0.149.1 screen; the clear probe reads the visible screen first and
	// only then pays for the `--extent=all` scrollback read, so both halves are
	// asserted.
	t.Run("clear recovery on the current build", func(t *testing.T) {
		cleared := readScreenFixture(t, "codex_cleared.txt")
		if !CodexScreenLooksCleared(cleared) {
			t.Fatal("CodexScreenLooksCleared() = false on a real cleared 0.149.1 screen")
		}
		if !ClassifyCodexClearScreen(cleared, cleared) {
			t.Fatal("ClassifyCodexClearScreen() = false on a real cleared 0.149.1 screen")
		}
		// A working pane is not a cleared pane, so the assertion above is not
		// passing on the widened composer alone.
		working := readScreenFixture(t, "codex_working.txt")
		if ClassifyCodexClearScreen(working, working) {
			t.Fatal("ClassifyCodexClearScreen() = true on a working screen")
		}
	})
}

// TestCodexCompletedMarkerRequiresItsFrame pins the framing requirement in
// isolation.
//
// "Completed." is one ordinary English word. Unlike "Worked for 1m 30s" it
// carries no duration to anchor on, so on its own it is a marker any assistant
// turn can print by accident — including one that is still running and merely
// narrating a finished sub-step. The horizontal rules Codex draws around it are
// the only thing that makes it harness-authored, and they are therefore
// required, not decorative.
func TestCodexCompletedMarkerRequiresItsFrame(t *testing.T) {
	if !ClassifyCodexCompletedTurnScreen(codexFramedCompletedScreen) {
		t.Fatal("ClassifyCodexCompletedTurnScreen() = false on the framed marker")
	}
	unframed := []struct {
		name   string
		screen string
	}{
		{"no rules at all", codexUnframedCompletedScreen},
		{"only a rule above", codexHalfFramedCompletedScreen},
		{"prose that merely says completed", codexCompletedProseScreen},
	}
	for _, tc := range unframed {
		t.Run(tc.name, func(t *testing.T) {
			if ClassifyCodexCompletedTurnScreen(tc.screen) {
				t.Fatal("ClassifyCodexCompletedTurnScreen() = true without the harness frame")
			}
			if got := codexObservation(t, tc.screen); got.SettleableTerminal() {
				t.Fatalf("SettleableTerminal() = true (state %s, vetoes %s)", got.State, got.Vetoes)
			}
		})
	}
}

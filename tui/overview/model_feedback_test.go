package overview

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/tui/commands"
)

// TestTheFeedbackRowDistinguishesTheLifecycleStates is the ranked sentence, one
// case per rank. The three the session names explicitly — still loading, no
// running sessions, and a failed FIRST load — are what stop a blank grid from
// meaning three different things at once.
func TestTheFeedbackRowDistinguishesTheLifecycleStates(t *testing.T) {
	t.Run("initial loading", func(t *testing.T) {
		m, _ := started(t, 0)
		if got := m.feedback(); got != "Loading live sessions…" {
			t.Fatalf("feedback = %q, want the loading sentence", got)
		}
		if m.loaded {
			t.Fatal("the surface reports itself loaded before any tree landed")
		}
	})

	t.Run("failed initial loading", func(t *testing.T) {
		m, _ := started(t, 0)
		m = only(t, m, commands.ErrMsg{Seq: firstRefreshSeq, Err: errors.New("daemon unreachable")})
		got := m.feedback()
		if !strings.HasPrefix(got, "Session refresh failed: ") || !strings.Contains(got, "daemon unreachable") {
			t.Fatalf("feedback = %q, want the cold failure and its reason", got)
		}
	})

	t.Run("no running sessions", func(t *testing.T) {
		m, _ := started(t, 0)
		m = only(t, m, snapshot(firstRefreshSeq, 0, nil))
		if got := m.feedback(); got != "No running sessions" {
			t.Fatalf("feedback = %q, want the no-sessions sentence", got)
		}
	})

	t.Run("a fresh non-empty list says nothing", func(t *testing.T) {
		m, _ := started(t, 0)
		m = only(t, m, snapshot(firstRefreshSeq, 0, viewSessions(3)))
		if got := m.feedback(); got != "" {
			t.Fatalf("feedback = %q, want silence", got)
		}
	})

	t.Run("a delayed refresh outranks emptiness", func(t *testing.T) {
		m, r := started(t, 0)
		m = only(t, m, snapshot(firstRefreshSeq, 0, nil))
		r.now = r.now.Add(protocol.TopologyStaleAfter)
		got := m.feedback()
		if !strings.HasPrefix(got, "Session refresh delayed") {
			t.Fatalf("feedback = %q, want the delay warning rather than a claim about an unrefreshable list", got)
		}
		if !strings.Contains(got, "6s old") {
			t.Fatalf("feedback = %q, want the age of the tree on screen", got)
		}
	})

	t.Run("a delayed refresh outranks a warm failure", func(t *testing.T) {
		m, r := started(t, 0)
		m = only(t, m, snapshot(firstRefreshSeq, 0, viewSessions(3)))
		m = only(t, m, commands.TickMsg{})
		m = only(t, m, commands.ErrMsg{Seq: m.refreshActiveSeq, Err: errors.New("down")})
		r.now = r.now.Add(20 * time.Second)
		if got := m.feedback(); !strings.HasPrefix(got, "Session refresh delayed") {
			t.Fatalf("feedback = %q; with rows on screen the age is what the operator needs, not the subprocess's words", got)
		}
	})

	t.Run("a focus failure outranks everything", func(t *testing.T) {
		sessions := viewSessions(3)
		m, r := loadedAt(t, sessions, 0)
		m = press(t, m, "enter")
		m = only(t, m, commands.AttachDoneMsg{Err: errors.New("no matching tab")})
		r.now = r.now.Add(time.Minute)
		if got := m.feedback(); !strings.HasPrefix(got, "Focus failed: ") {
			t.Fatalf("feedback = %q, want the operator's own failed action first", got)
		}
	})
}

// TestTheFeedbackRowReachesTheFrame. The ranking is only worth having if the
// sentence is drawn, and the renderer reserves exactly one row for it.
func TestTheFeedbackRowReachesTheFrame(t *testing.T) {
	m, _ := started(t, 0)
	m = only(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	m = only(t, m, commands.ErrMsg{Seq: firstRefreshSeq, Err: errors.New("daemon unreachable")})

	lines := strings.Split(frameOf(m), "\n")
	if len(lines) != 30 {
		t.Fatalf("the frame is %d lines, want 30", len(lines))
	}
	if !strings.Contains(lines[1], "daemon unreachable") {
		t.Fatalf("the reserved feedback row says %q, want the failure", lines[1])
	}
	if !strings.Contains(lines[len(lines)-1], "enter focus") {
		t.Fatalf("the hints row says %q, want the overview's own hints", lines[len(lines)-1])
	}
}

// TestTheDisplayedAgeIncludesWhatTheTreeAlreadyCarried is why the elapsed term is
// added to the snapshot's own age rather than replacing it: the daemon's tree was
// already some seconds old when it was published, and an overview that ignored
// that would under-report every reading it took from the daemon.
func TestTheDisplayedAgeIncludesWhatTheTreeAlreadyCarried(t *testing.T) {
	m, r := started(t, 0)
	msg := snapshot(firstRefreshSeq, 0, viewSessions(2))
	msg.Topology.Age = 4 * time.Second
	m = only(t, m, msg)

	if age, warm := m.topologyAge(); !warm || age != 4*time.Second {
		t.Fatalf("the age at the moment of application is %v/%t, want the 4s the tree carried", age, warm)
	}
	r.now = r.now.Add(3 * time.Second)
	if age, _ := m.topologyAge(); age != 7*time.Second {
		t.Fatalf("the age after 3s is %v, want 7s", age)
	}
	if got := m.feedback(); !strings.Contains(got, "7s old") {
		t.Fatalf("feedback = %q, want the combined age", got)
	}

	// A newer tree resets the reading, even one the daemon marked stale.
	m = only(t, m, commands.TickMsg{})
	fresher := snapshot(m.refreshActiveSeq, 0, viewSessions(2))
	fresher.Topology.Stale = true
	m = only(t, m, fresher)
	if age, _ := m.topologyAge(); age != 0 {
		t.Fatalf("a newer tree left the age at %v, want it reset", age)
	}
	if got := m.feedback(); got != "" {
		t.Fatalf("feedback = %q after a newer tree, want silence", got)
	}
}

// TestABackwardsClockIsClampedRatherThanReportedNegative covers the machine whose
// clock stepped back between a read and a frame.
func TestABackwardsClockIsClampedRatherThanReportedNegative(t *testing.T) {
	m, r := started(t, 0)
	m = only(t, m, snapshot(firstRefreshSeq, 0, viewSessions(2)))
	r.now = r.now.Add(-time.Hour)
	age, warm := m.topologyAge()
	if !warm {
		t.Fatal("a backwards clock made a warm surface look cold")
	}
	if age != 0 {
		t.Fatalf("the age is %v, want it clamped to zero", age)
	}
}

// TestTheModelStartsNoDashboardWarmupWork is the negative half of "keep it a
// standalone sibling". A model that had also initialized the dashboard's caches
// would still render a grid, so the assertion is on the command count: Init's two
// commands are the read and the ticker, and nothing else.
func TestTheModelStartsNoDashboardWarmupWork(t *testing.T) {
	m, r := newTestModel(0)
	if cmd := m.Init(); cmd == nil {
		t.Fatal("Init returned no command")
	}
	if len(r.topology) != 1 || len(r.ticks) != 1 {
		t.Fatalf("Init produced %d reads and %d ticks, want exactly one of each", len(r.topology), len(r.ticks))
	}
	if len(r.focus) != 0 {
		t.Fatalf("Init dispatched %d focus actions, want 0", len(r.focus))
	}
}

// TestEveryTileIsLoadingUntilCapturesArrive is the intentionally pending
// capability, pinned rather than left implicit: the model holds an empty preview
// map, so every tile draws the honest loading state until session 4 fills it.
func TestEveryTileIsLoadingUntilCapturesArrive(t *testing.T) {
	sessions := viewSessions(4)
	m, _ := loadedAt(t, sessions, 0)
	m = only(t, m, tea.WindowSizeMsg{Width: 160, Height: 45})
	if len(m.previews) != 0 {
		t.Fatalf("the model holds %d captures; session 3 supplies none", len(m.previews))
	}

	frame := frameOf(m)
	if got := strings.Count(frame, "(loading…)"); got != len(sessions) {
		t.Fatalf("%d tiles show the loading state, want all %d:\n%s", got, len(sessions), frame)
	}
}

// TestTheModelRendersEveryShippedTerminalSize ties the lifecycle to the renderer's
// size contract: whatever state the model is in, the frame it produces is exactly
// the surface it was given.
func TestTheModelRendersEveryShippedTerminalSize(t *testing.T) {
	for _, size := range shippedSizes {
		for _, count := range []int{0, 1, 5, 12} {
			m, _ := started(t, 0)
			m = only(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			m = only(t, m, snapshot(firstRefreshSeq, 0, viewSessions(count)))
			m = press(t, m, "end")
			lines := strings.Split(Render(overviewInput(m)), "\n")
			if len(lines) != size[1] {
				t.Fatalf("%dx%d with %d sessions rendered %d lines", size[0], size[1], count, len(lines))
			}
		}
	}
}

// TestAFrameBeforeTheFirstResizeIsEmptyRatherThanInvented. Bubble Tea delivers the
// window size as a message, so there is one frame's worth of time in which the
// surface has no dimensions. Rendering some assumed 80x24 there would flash a
// layout the terminal is not.
func TestAFrameBeforeTheFirstResizeIsEmptyRatherThanInvented(t *testing.T) {
	m, _ := started(t, 0)
	if m.width != 0 || m.height != 0 {
		t.Fatalf("the model starts at %dx%d, want an unknown surface", m.width, m.height)
	}
	if got := Render(overviewInput(m)); got != "" {
		t.Fatalf("a surface of unknown size rendered %q", got)
	}
}

// TestTheOverviewsDefaultsMatchTheConfigurationsOwn guards the two constants the
// overview restates rather than imports. A silent divergence between the two
// surfaces' cadences would be invisible on either of them.
func TestTheOverviewsDefaultsMatchTheConfigurationsOwn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("writing an empty config: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("loading an empty config: %v", err)
	}
	if want := time.Duration(cfg.Defaults.RefreshInterval) * time.Second; want != defaultRefreshInterval {
		t.Errorf("the overview falls back to %v, want the configuration's own default of %v", defaultRefreshInterval, want)
	}
	if want := time.Duration(cfg.Defaults.IdleThreshold) * time.Second; want != defaultIdleThreshold {
		t.Errorf("the overview falls back to an idle threshold of %v, want the configuration's %v", defaultIdleThreshold, want)
	}

	// And an unset configuration reaches those fallbacks through the constructor.
	m := newModel(&config.Config{}, nil, 0)
	if m.interval != defaultRefreshInterval || m.idleThreshold != defaultIdleThreshold {
		t.Errorf("an unset config produced %v/%v, want %v/%v", m.interval, m.idleThreshold, defaultRefreshInterval, defaultIdleThreshold)
	}
	if !m.compact || !cfg.Defaults.Overview.CompactValue() {
		t.Errorf("an unset config opened compact=%v (loaded %v), want the compact default", m.compact, cfg.Defaults.Overview.CompactValue())
	}
}

// TestTheConfiguredModeIsTheOpeningMode pins how [defaults.overview] compact and
// the daemon's remembered toggle combine: the configuration decides until the
// operator has pressed `c` during this daemon's lifetime, and a late read still
// cannot undo a key pressed in this overview.
func TestTheConfiguredModeIsTheOpeningMode(t *testing.T) {
	for _, configured := range []bool{true, false} {
		cfg := viewConfig()
		cfg.Defaults.Overview.Compact = &configured
		m := newModel(cfg, nil, 0)
		if m.compact != configured {
			t.Fatalf("configured compact=%v opened compact=%v", configured, m.compact)
		}

		// No toggle yet this daemon lifetime: the daemon's zero value is not a choice.
		next, _ := m.handlePreferences(commands.OverviewPreferencesMsg{Read: true, Compact: !configured})
		if got := next.(model).compact; got != configured {
			t.Errorf("configured compact=%v: a non-explicit read switched to %v", configured, got)
		}

		// An operator toggle remembered by the daemon wins over the configuration.
		next, _ = m.handlePreferences(commands.OverviewPreferencesMsg{Read: true, Explicit: true, Compact: !configured})
		if got := next.(model).compact; got != !configured {
			t.Errorf("configured compact=%v: an explicit read left compact=%v", configured, got)
		}

		// A key pressed before the read lands is never undone by it.
		toggled, _ := m.toggleCompact()
		next, _ = toggled.(model).handlePreferences(commands.OverviewPreferencesMsg{Read: true, Explicit: true, Compact: configured})
		if got := next.(model).compact; got != !configured {
			t.Errorf("configured compact=%v: a late read undid the toggle (compact=%v)", configured, got)
		}
	}
}

// Tmux tiles kept through an outage are announced as kept, and the sentence
// goes away with the outage.
func TestFeedbackNamesATmuxOutageWhileItsTilesAreKept(t *testing.T) {
	m, _ := started(t, 0)
	kept := snapshot(firstRefreshSeq, 0, viewSessions(2))
	kept.PortableErr = &commands.PortableOutage{Err: errors.New("timeout"), Kept: 2}
	m = only(t, m, kept)
	if len(m.sessions) != 2 {
		t.Fatalf("%d tiles; want the 2 kept", len(m.sessions))
	}
	if got := m.feedback(); !strings.HasPrefix(got, "tmux outage: tmux is not answering") || !strings.Contains(got, "2 sessions") {
		t.Fatalf("feedback = %q; want the outage and what is kept", got)
	}
	m = only(t, m, commands.TickMsg{})
	m = only(t, m, snapshot(m.refreshActiveSeq, 0, viewSessions(2)))
	if got := m.feedback(); got != "" {
		t.Fatalf("feedback after recovery = %q; want silence", got)
	}
}

// An overview that is a popup over a terminal switched that terminal to the
// tile chosen, so it gets out of the way; an attached view that came back, or
// a switch that was refused, leaves it up.
func TestAPopupOverviewEndsOnceItsTerminalIsSwitched(t *testing.T) {
	m, _ := newTestModel(0)
	m.focusTabID = 1
	if _, cmd := m.Update(commands.AttachDoneMsg{Switched: true}); !quits(t, cmd) {
		t.Fatal("the popup stayed over the session it switched the terminal to")
	}
	if _, cmd := m.Update(commands.AttachDoneMsg{}); quits(t, cmd) {
		t.Fatal("returning from an attached session ended the overview")
	}
	failure := errors.New("the agent exited")
	next, cmd := m.Update(commands.AttachDoneMsg{Err: failure})
	if quits(t, cmd) || !errors.Is(next.(model).focusErr, failure) {
		t.Fatalf("refused switch: quit=%v err=%v, want the reason shown and the overview kept", quits(t, cmd), next.(model).focusErr)
	}
}

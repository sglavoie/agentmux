package daemon

// The decline counter is the health signal the screen classifier never had.
// When a harness redesigns its TUI the regexes stop matching, every probe
// declines, and the affected tab holds ⚡ forever — with one slog.Debug line as
// the only symptom. These tests pin the counter's whole contract: it climbs on
// repeated declines, it restarts whenever the screen underneath it changes (so
// a pane that is genuinely working never accumulates), it distinguishes a quiet
// vetoed screen from a repainting one, a settle that actually lands clears it,
// and a closed window takes its streaks with it.

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

// screenDeclineState reads one probe source's raw decline state straight out of
// the maps under stateMu. Tests assert on the stored values rather than only on
// what recordScreenDeclineLocked returned, so a helper that reports a streak it
// never persisted fails here.
func screenDeclineState(t *testing.T, h *Handler, windowID int, source SourceID) (streak int, fingerprint int64, present bool) {
	t.Helper()
	ref := idleSampleRef{windowID: windowID, key: source.Key}
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	streak, present = h.screenProbeDeclineStreak[ref]
	fingerprint = h.screenProbeDeclineFingerprint[ref]
	return streak, fingerprint, present
}

// TestRecordScreenDeclineLockedStreakAndStability walks the counter through
// every transition it has. It runs against a bare Handler literal on purpose:
// that is the nil-map case the write-site guards exist for, and the pattern
// recordIdleSampleLocked already follows because tests construct Handlers
// without NewHandler.
func TestRecordScreenDeclineLockedStreakAndStability(t *testing.T) {
	h := &Handler{}
	ref := idleSampleRef{windowID: 7, key: "codex_screen"}

	record := func(fingerprint int64) ScreenDeclineObservation {
		t.Helper()
		h.stateMu.Lock()
		defer h.stateMu.Unlock()
		return h.recordScreenDeclineLocked(ref, fingerprint)
	}

	steps := []struct {
		name        string
		fingerprint int64
		wantStreak  int
		wantStable  bool
	}{
		// A quiet screen the classifier keeps refusing: the streak climbs and
		// becomes stable as soon as the SAME frame has been declined twice.
		{"first decline", 0x1111, 1, false},
		{"same screen again", 0x1111, 2, true},
		{"same screen a third time", 0x1111, 3, true},
		// A repainting pane: the fingerprint moves, so the streak restarts and
		// the stability flag goes back down. This is what ordinary live work
		// looks like and it is why the raw count alone is not a signal.
		{"screen repainted", 0x2222, 1, false},
		{"repainted screen holds", 0x2222, 2, true},
		{"repainted again", 0x3333, 1, false},
		// A fail-closed zero Observation carries no hash at all, so it can
		// never be called stable no matter how often it repeats.
		{"unclassifiable screen", 0, 1, false},
		{"unclassifiable screen repeats", 0, 2, false},
	}
	for _, step := range steps {
		got := record(step.fingerprint)
		if got.Streak != step.wantStreak {
			t.Fatalf("%s: streak = %d, want %d", step.name, got.Streak, step.wantStreak)
		}
		if got.StableFingerprint != step.wantStable {
			t.Fatalf("%s: stable fingerprint = %t, want %t", step.name, got.StableFingerprint, step.wantStable)
		}
		if got.Fingerprint != step.fingerprint {
			t.Fatalf("%s: fingerprint = %d, want %d", step.name, got.Fingerprint, step.fingerprint)
		}
		h.stateMu.Lock()
		storedStreak := h.screenProbeDeclineStreak[ref]
		storedFingerprint := h.screenProbeDeclineFingerprint[ref]
		h.stateMu.Unlock()
		if storedStreak != step.wantStreak || storedFingerprint != step.fingerprint {
			t.Fatalf("%s: stored (streak=%d fingerprint=%d), want (streak=%d fingerprint=%d)",
				step.name, storedStreak, storedFingerprint, step.wantStreak, step.fingerprint)
		}
	}
}

// TestRecordScreenProbeDeclineResetsIdleSampleAtomically pins the reason the
// decline credit went behind one wrapper rather than beside the existing
// ClearIdleSample call: the stabilization reset and the decline record are the
// two halves of one observation and must land under a single stateMu
// acquisition.
func TestRecordScreenProbeDeclineResetsIdleSampleAtomically(t *testing.T) {
	h := newTestHandler()
	source := SourceID{Kind: SourceProbe, Key: screenPromptProbeSource}
	ref := idleSampleRef{windowID: 7, key: source.Key}

	// Bank a stabilization streak with a generation, exactly as a run of
	// agreeing settleable frames would.
	h.stateMu.Lock()
	if n := h.recordIdleSampleWithGenerationLocked(ref, 0x4242); n != 1 {
		h.stateMu.Unlock()
		t.Fatalf("seeded idle sample = %d, want 1", n)
	}
	h.stateMu.Unlock()

	got := h.RecordScreenProbeDecline(7, source, 0x1111)
	if got.Streak != 1 || got.StableFingerprint {
		t.Fatalf("first decline = %+v, want streak 1 and an unstable fingerprint", got)
	}

	h.stateMu.Lock()
	idleStreak, idlePresent := h.idleSampleStreak[ref]
	_, genPresent := h.idleSampleGen[ref]
	h.stateMu.Unlock()
	if idlePresent || genPresent {
		t.Fatalf("decline left idle stabilization state behind: streak=%d present=%t gen_present=%t",
			idleStreak, idlePresent, genPresent)
	}

	if got := h.RecordScreenProbeDecline(7, source, 0x1111); got.Streak != 2 || !got.StableFingerprint {
		t.Fatalf("second decline on the same screen = %+v, want streak 2 and a stable fingerprint", got)
	}

	// A successful settle is the classifier proving it can still grade this
	// pane, so the streak restarts from nothing.
	h.ClearScreenProbeDecline(7, source)
	if _, _, present := screenDeclineState(t, h, 7, source); present {
		t.Fatalf("ClearScreenProbeDecline left a streak behind")
	}
	if got := h.RecordScreenProbeDecline(7, source, 0x1111); got.Streak != 1 || got.StableFingerprint {
		t.Fatalf("decline after clear = %+v, want streak 1 and an unstable fingerprint", got)
	}
}

// TestScreenProbeDeclineStreaksDoNotCollide covers the reason idleSampleRef is
// the right key: one window can run both harnesses' probe sources, and two
// windows can be declining independently.
func TestScreenProbeDeclineStreaksDoNotCollide(t *testing.T) {
	h := newTestHandler()
	codex := SourceID{Kind: SourceProbe, Key: screenPromptProbeSource}
	claude := SourceID{Kind: SourceProbe, Key: claudeScreenStopProbeSource}

	h.RecordScreenProbeDecline(7, codex, 0x1111)
	h.RecordScreenProbeDecline(7, codex, 0x1111)
	if got := h.RecordScreenProbeDecline(7, claude, 0x9999); got.Streak != 1 {
		t.Fatalf("claude streak on the same window = %d, want its own count of 1", got.Streak)
	}
	if got := h.RecordScreenProbeDecline(8, codex, 0x1111); got.Streak != 1 {
		t.Fatalf("codex streak on a second window = %d, want its own count of 1", got.Streak)
	}
	if streak, _, _ := screenDeclineState(t, h, 7, codex); streak != 2 {
		t.Fatalf("codex streak on window 7 = %d, want 2 after the other refs recorded", streak)
	}
}

// TestScreenProbeDeclineStreakPurgedOnWindowClose is the leak guard. A closed
// pane is never classified again, so a streak left behind would be a count
// frozen forever at whatever it reached.
func TestScreenProbeDeclineStreakPurgedOnWindowClose(t *testing.T) {
	h := newTestHandler()
	source := SourceID{Kind: SourceProbe, Key: screenPromptProbeSource}
	// Give both windows live state so window-closure GC recognizes them.
	idleSeedHarness(t, h, 7, "cur-7", "active", time.Now().Unix())
	idleSeedHarness(t, h, 8, "cur-8", "active", time.Now().Unix())

	h.RecordScreenProbeDecline(7, source, 0x1111)
	h.RecordScreenProbeDecline(7, source, 0x1111)
	h.RecordScreenProbeDecline(8, source, 0x2222)

	// Kitty still reports window 8, so only window 7's streak goes.
	h.pruneClosedWindows(time.Now(), map[int]bool{8: true})

	if streak, fingerprint, present := screenDeclineState(t, h, 7, source); present {
		t.Fatalf("closed window kept its decline streak: streak=%d fingerprint=%d", streak, fingerprint)
	}
	if streak, fingerprint, present := screenDeclineState(t, h, 8, source); !present || streak != 1 || fingerprint != 0x2222 {
		t.Fatalf("live window lost its decline streak: streak=%d fingerprint=%d present=%t", streak, fingerprint, present)
	}
}

// TestProbeCodexScreenDeclineStreakThroughTheProbe drives the real probe rather
// than the helper, which is what proves the counter is wired to the decline the
// comment at the decline site describes — and not merely to a function no
// production path calls. A working Codex pane is declined on every tick, and
// because the fixture is a static frame the fingerprint is stable, which is
// exactly the shape a classifier that has stopped matching would produce.
func TestProbeCodexScreenDeclineStreakThroughTheProbe(t *testing.T) {
	base := time.Now()
	source := SourceID{Kind: SourceProbe, Key: screenPromptProbeSource}
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
			Profile: "work",
			Harness: harness.Codex,
		}),
		getText: map[string]string{kitty.MatchWindowByID(42): codexWorkingTurnScreen},
	}
	h := newTestHandler()
	h.SetKittyClient(fake)
	codexSeedActiveBolt(t, h, 42, "sess-declining", base.Unix())

	const declines = 4
	for tick := 0; tick < declines; tick++ {
		h.ProbeCodexScreenPrompts(base.Add(time.Duration(tick) * time.Second))
		streak, fingerprint, present := screenDeclineState(t, h, 42, source)
		if !present || streak != tick+1 {
			t.Fatalf("decline streak after tick %d = %d (present=%t), want %d", tick, streak, present, tick+1)
		}
		if fingerprint == 0 {
			t.Fatalf("decline after tick %d recorded no fingerprint", tick)
		}
	}
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
		t.Fatalf("effective across %d declines = %+v, want the working bolt held", declines, eff)
	}

	// The turn finishes: the completed-turn frame repeats until the
	// stabilization run settles, and the settle wipes the decline history.
	fake.mu.Lock()
	fake.getText[kitty.MatchWindowByID(42)] = codexCompletedTurnScreen
	fake.mu.Unlock()
	settleBase := base.Add(time.Duration(declines) * time.Second)
	probeCodexUntilSettled(t, h, settleBase)

	if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
		t.Fatalf("effective after the stabilization run = %+v, want stop", eff)
	}
	if streak, fingerprint, present := screenDeclineState(t, h, 42, source); present {
		t.Fatalf("settle left a decline streak behind: streak=%d fingerprint=%d", streak, fingerprint)
	}
}

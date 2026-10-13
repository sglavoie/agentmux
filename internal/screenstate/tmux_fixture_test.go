package screenstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"
)

// The tmux captures under testdata/tmux were taken with testdata/tmux/capture.sh
// against live panes on a private tmux 3.7c server running the production
// configuration (internal/tmux.Config): Claude Code 2.1.296, codex-cli 0.162.0
// (codex_cleared: 0.162.1) and GitHub Copilot CLI 1.0.95. Each NAME.txt is `capture-pane -p` byte for
// byte — what the tmux backend's Capture and Observe return — NAME.ansi is the
// same screen with `-e`, and NAME.json is the pane geometry and screen mode
// tmux reported for it. None is hand-edited; re-take a capture when a harness
// release moves the copy.
//
// Three later captures feed the probes that read more than a screen. The two
// claude_*_titled ones carry NAME.title, the pane title Claude had set
// (pane_title, which the backend's Observe reads with the screen), and
// codex_cleared carries NAME.all.txt, the screen with its history
// (`capture-pane -p -S - -E -`, the backend's Capture(ExtentAll)), taken from
// codex-cli 0.162.1 after a turn and a /clear.
//
// They are the second fixture set. The captures directly under testdata are
// Kitty `get-text --extent=screen` reads, and the tests here hold the two sets
// to the same readings.
const tmuxFixtureRoot = "testdata/tmux"

// Behaviours the plan requires each backend's fixtures to prove.
const (
	behaviourPrompt     = "prompt"     // sitting at a prompt for the user
	behaviourPermission = "permission" // a tool permission dialog
	behaviourBusy       = "busy"       // a live turn
	behaviourCompletion = "completion" // a finished turn
	behaviourFailure    = "failure"    // interrupted, rejected, or exited
)

type tmuxFixture struct {
	name      string
	harness   string
	behaviour string
	state     State
	tier      Tier
	vetoes    Veto
	cols      int
	rows      int
	alternate bool
}

// tmuxFixtures registers every capture. Nothing discovers fixtures, so
// TestTmuxFixturesAreAllRegistered fails on a file missing from this table.
var tmuxFixtures = []tmuxFixture{
	// Claude Code 2.1.296.
	{"claude_trust_prompt", "claude", behaviourPrompt, Blocked, TierNone, VetoBlockingPrompt | VetoComposerBusy, 120, 36, false},
	{"claude_idle_bare_composer", "claude", behaviourPrompt, Unknown, TierNone, VetoComposerBusy, 120, 36, true},
	{"claude_permission_prompt", "claude", behaviourPermission, Blocked, TierNone, VetoBlockingPrompt | VetoComposerBusy, 120, 36, true},
	{"claude_permission_prompt_narrow", "claude", behaviourPermission, Blocked, TierNone, VetoBlockingPrompt | VetoComposerBusy, 64, 30, true},
	{"claude_working_t0", "claude", behaviourBusy, Working, TierNone, VetoLiveStatusLine, 120, 36, true},
	{"claude_working_t1", "claude", behaviourBusy, Working, TierNone, VetoLiveStatusLine, 120, 36, true},
	{"claude_working_no_timer", "claude", behaviourBusy, Working, TierNone, VetoLiveStatusLine, 120, 36, true},
	{"claude_working_streaming_t0", "claude", behaviourBusy, Terminal, TierQuiescent, 0, 120, 36, true},
	{"claude_working_streaming_t1", "claude", behaviourBusy, Terminal, TierQuiescent, 0, 120, 36, true},
	{"claude_idle_completed_t0", "claude", behaviourCompletion, Terminal, TierQuiescent, 0, 120, 36, true},
	{"claude_idle_completed_t1", "claude", behaviourCompletion, Terminal, TierQuiescent, 0, 120, 36, true},
	{"claude_interrupted", "claude", behaviourFailure, Terminal, TierTerminalExplicit, 0, 120, 36, true},
	{"claude_rejected_tool", "claude", behaviourFailure, Terminal, TierQuiescent, 0, 120, 36, true},
	{"claude_interrupted_restored_input", "claude", behaviourFailure, Unknown, TierNone, VetoComposerBusy, 120, 36, true},
	{"claude_exited_pane_dead", "claude", behaviourFailure, Blocked, TierNone, VetoBlockingPrompt | VetoComposerBusy, 120, 36, false},
	{"claude_working_streaming_titled", "claude", behaviourBusy, Terminal, TierQuiescent, 0, 120, 36, true},
	{"claude_idle_completed_titled", "claude", behaviourCompletion, Terminal, TierQuiescent, 0, 120, 36, true},

	// codex-cli 0.162.0.
	{"codex_trust_prompt", "codex", behaviourPrompt, Unknown, TierNone, VetoComposerBusy, 120, 36, true},
	{"codex_idle_bare_composer", "codex", behaviourPrompt, Unknown, TierNone, 0, 120, 36, true},
	{"codex_permission_prompt", "codex", behaviourPermission, Blocked, TierNone, VetoInterruptHint | VetoLiveStatusLine | VetoBlockingPrompt | VetoComposerBusy, 120, 36, true},
	{"codex_permission_prompt_narrow", "codex", behaviourPermission, Blocked, TierNone, VetoInterruptHint | VetoLiveStatusLine | VetoBlockingPrompt | VetoComposerBusy, 64, 30, true},
	{"codex_working_t0", "codex", behaviourBusy, Working, TierNone, VetoInterruptHint | VetoLiveStatusLine, 120, 36, true},
	{"codex_working_t1", "codex", behaviourBusy, Working, TierNone, VetoInterruptHint | VetoLiveStatusLine, 120, 36, true},
	{"codex_idle_completed_t0", "codex", behaviourCompletion, Terminal, TierTerminalExplicit, 0, 120, 36, true},
	{"codex_idle_completed_t1", "codex", behaviourCompletion, Terminal, TierTerminalExplicit, 0, 120, 36, true},
	{"codex_rejected_command", "codex", behaviourFailure, Terminal, TierTerminalExplicit, 0, 120, 36, true},
	{"codex_cleared", "codex", behaviourPrompt, Unknown, TierNone, 0, 120, 36, true},

	// GitHub Copilot CLI 1.0.95. Its adapter has no Terminal branch: the
	// screen never reports a finished Copilot turn on either backend.
	{"copilot_trust_prompt", "copilot", behaviourPrompt, Blocked, TierNone, VetoBlockingPrompt | VetoComposerBusy, 120, 36, true},
	{"copilot_idle_bare_composer", "copilot", behaviourPrompt, Unknown, TierNone, 0, 120, 36, true},
	{"copilot_permission_prompt", "copilot", behaviourPermission, Blocked, TierNone, VetoBlockingPrompt | VetoComposerBusy, 120, 36, true},
	{"copilot_permission_prompt_narrow", "copilot", behaviourPermission, Blocked, TierNone, VetoBlockingPrompt | VetoComposerBusy, 64, 30, true},
	{"copilot_working_t0", "copilot", behaviourBusy, Working, TierNone, VetoLiveStatusLine, 120, 36, true},
	{"copilot_working_t1", "copilot", behaviourBusy, Working, TierNone, VetoLiveStatusLine, 120, 36, true},
	{"copilot_working_streaming", "copilot", behaviourBusy, Working, TierNone, VetoInterruptHint | VetoLiveStatusLine, 120, 36, true},
	{"copilot_working_interrupt_hint", "copilot", behaviourBusy, Working, TierNone, VetoInterruptHint | VetoLiveStatusLine, 120, 36, true},
	{"copilot_idle_completed_t0", "copilot", behaviourCompletion, Unknown, TierNone, 0, 120, 36, true},
	{"copilot_idle_completed_t1", "copilot", behaviourCompletion, Unknown, TierNone, 0, 120, 36, true},
	{"copilot_interrupted", "copilot", behaviourFailure, Unknown, TierNone, 0, 120, 36, true},
	{"copilot_rejected_command", "copilot", behaviourFailure, Unknown, TierNone, 0, 120, 36, true},
}

var tmuxFixtureVersions = map[string]string{
	"claude":  "2.1.296",
	"codex":   "0.162.0",
	"copilot": "1.0.95",
}

// tmuxFixtureVersionOf names the captures taken from a later build than the
// rest of their harness's set.
var tmuxFixtureVersionOf = map[string]string{
	"codex_cleared": "0.162.1",
}

func tmuxFixtureVersion(fixture tmuxFixture) string {
	if version, ok := tmuxFixtureVersionOf[fixture.name]; ok {
		return version
	}
	return tmuxFixtureVersions[fixture.harness]
}

// tmuxFixtureExtras names what a capture carries beside its screen: the pane
// title (capture.sh title) or the screen with its history (capture.sh all).
var tmuxFixtureExtras = map[string]string{
	"claude_working_streaming_titled": ".title",
	"claude_idle_completed_titled":    ".title",
	"codex_cleared":                   ".all.txt",
}

// tmuxFixtureMeta is the NAME.json sidecar capture.sh writes from tmux formats.
type tmuxFixtureMeta struct {
	Harness     string `json:"harness"`
	Version     string `json:"version"`
	Tmux        string `json:"tmux"`
	Cols        int    `json:"cols"`
	Rows        int    `json:"rows"`
	AlternateOn int    `json:"alternate_on"`
	Capture     string `json:"capture"`
}

func readTmuxFixture(t *testing.T, name, ext string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(tmuxFixtureRoot, name+ext))
	if err != nil {
		t.Fatalf("read tmux fixture %s%s: %v", name, ext, err)
	}
	return string(data)
}

func classifyFixture(t *testing.T, harnessName, screen string) Observation {
	t.Helper()
	adapter, ok := For(harnessName)
	if !ok {
		t.Fatalf("For(%q) returned no adapter", harnessName)
	}
	return adapter.Classify(screen)
}

func TestTmuxFixturesAreAllRegistered(t *testing.T) {
	want := map[string]bool{"capture.sh": true}
	for _, fixture := range tmuxFixtures {
		for _, ext := range []string{".txt", ".ansi", ".json"} {
			want[fixture.name+ext] = true
		}
	}
	for name, ext := range tmuxFixtureExtras {
		want[name+ext] = true
	}
	entries, err := os.ReadDir(tmuxFixtureRoot)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, entry := range entries {
		got[entry.Name()] = true
		if !want[entry.Name()] {
			t.Errorf("%s is committed but no test names it", entry.Name())
		}
	}
	for name := range want {
		if !got[name] {
			t.Errorf("%s is registered but not committed", name)
		}
	}
}

func TestTmuxFixtureClassification(t *testing.T) {
	for _, fixture := range tmuxFixtures {
		t.Run(fixture.name, func(t *testing.T) {
			got := classifyFixture(t, fixture.harness, readTmuxFixture(t, fixture.name, ".txt"))
			if got.State != fixture.state || got.Tier != fixture.tier || got.Vetoes != fixture.vetoes {
				t.Errorf("classified %s/%s/%s, want %s/%s/%s",
					got.State, got.Tier, got.Vetoes, fixture.state, fixture.tier, fixture.vetoes)
			}
			if got.Fingerprint == 0 {
				t.Error("zero fingerprint")
			}
			// Only a screen filed as a finished or failed turn may settle; the
			// streaming pair is the named exception with its own test below.
			settles := fixture.behaviour == behaviourCompletion || fixture.behaviour == behaviourFailure ||
				strings.HasPrefix(fixture.name, "claude_working_streaming")
			if got.SettleableTerminal() && !settles {
				t.Errorf("a %s screen settled as a finished turn", fixture.behaviour)
			}
		})
	}
}

// TestTmuxFixturesCoverEveryBehaviour is the coverage half of the plan's
// claim: all three harnesses, all five behaviours, from real tmux panes.
func TestTmuxFixturesCoverEveryBehaviour(t *testing.T) {
	seen := map[string]bool{}
	for _, fixture := range tmuxFixtures {
		seen[fixture.harness+"/"+fixture.behaviour] = true
	}
	for harnessName := range tmuxFixtureVersions {
		for _, behaviour := range []string{
			behaviourPrompt, behaviourPermission, behaviourBusy, behaviourCompletion, behaviourFailure,
		} {
			if !seen[harnessName+"/"+behaviour] {
				t.Errorf("no tmux capture of a %s %s screen", harnessName, behaviour)
			}
		}
	}
}

// TestTmuxFixtureGeometry holds each capture to the pane tmux said it came
// from: one line per row, no row wider than the pane, and the recorded screen
// mode. All three harnesses draw their TUI on the alternate screen, so these
// captures are alternate-screen reads; Claude's pre-TUI trust dialog and the
// dead pane are the primary-screen ones.
func TestTmuxFixtureGeometry(t *testing.T) {
	alternate, primary := 0, 0
	for _, fixture := range tmuxFixtures {
		t.Run(fixture.name, func(t *testing.T) {
			var meta tmuxFixtureMeta
			if err := json.Unmarshal([]byte(readTmuxFixture(t, fixture.name, ".json")), &meta); err != nil {
				t.Fatalf("sidecar: %v", err)
			}
			if meta.Harness != fixture.harness || meta.Version != tmuxFixtureVersion(fixture) {
				t.Errorf("sidecar says %s %s, want %s %s",
					meta.Harness, meta.Version, fixture.harness, tmuxFixtureVersion(fixture))
			}
			if meta.Capture != "capture-pane -p" || meta.Tmux == "" {
				t.Errorf("sidecar capture=%q tmux=%q", meta.Capture, meta.Tmux)
			}
			if meta.Cols != fixture.cols || meta.Rows != fixture.rows || (meta.AlternateOn == 1) != fixture.alternate {
				t.Errorf("sidecar %dx%d alternate=%d, want %dx%d alternate=%v",
					meta.Cols, meta.Rows, meta.AlternateOn, fixture.cols, fixture.rows, fixture.alternate)
			}
			screen := readTmuxFixture(t, fixture.name, ".txt")
			if rows := strings.Count(screen, "\n"); rows != meta.Rows {
				t.Errorf("capture has %d rows, pane had %d", rows, meta.Rows)
			}
			for i, line := range strings.Split(screen, "\n") {
				if width := runewidth.StringWidth(line); width > meta.Cols {
					t.Errorf("row %d is %d cells wide on a %d-column pane: %q", i, width, meta.Cols, line)
				}
			}
			if fixture.alternate {
				alternate++
			} else {
				primary++
			}
		})
	}
	if alternate == 0 || primary == 0 {
		t.Errorf("captures cover %d alternate-screen and %d primary-screen panes; want both", alternate, primary)
	}
}

// TestTmuxAnsiCapturesReadLikePlainOnes pins ANSI handling: the styled capture
// of a screen, stripped, is the plain capture of it — same lines, same
// fingerprint, same reading.
func TestTmuxAnsiCapturesReadLikePlainOnes(t *testing.T) {
	styled := 0
	for _, fixture := range tmuxFixtures {
		t.Run(fixture.name, func(t *testing.T) {
			plain := readTmuxFixture(t, fixture.name, ".txt")
			ansi := readTmuxFixture(t, fixture.name, ".ansi")
			if strings.ContainsRune(plain, '\x1b') {
				t.Fatal("the plain capture carries escape sequences")
			}
			if strings.ContainsRune(ansi, '\x1b') {
				styled++
			}
			stripped := StripANSI(ansi)
			if strings.ContainsRune(stripped, '\x1b') {
				t.Fatal("StripANSI left an escape byte behind")
			}
			if !reflect.DeepEqual(NormalizeLines(stripped), NormalizeLines(plain)) {
				t.Error("stripped styled capture differs from the plain capture")
			}
			if Fingerprint(stripped) != Fingerprint(plain) {
				t.Error("fingerprints differ")
			}
			got, want := classifyFixture(t, fixture.harness, stripped), classifyFixture(t, fixture.harness, plain)
			if got != want {
				t.Errorf("stripped capture reads %+v, plain reads %+v", got, want)
			}
		})
	}
	if styled < 20 {
		t.Errorf("only %d styled captures contain escape sequences; the comparison is close to vacuous", styled)
	}
}

func TestStripANSI(t *testing.T) {
	cases := map[string]string{
		"plain text\n":                                        "plain text\n",
		"\x1b[1;31mred\x1b[0m\n":                              "red\n",
		"\x1b[38;2;10;20;30mrgb\x1b[39m":                      "rgb",
		"a\x1b[2Kb\x1b[10;5Hc":                                "abc",
		"\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\": "link",
		"\x1b]0;title\atext":                                  "text",
		"\x1b(Bascii\x1b=":                                    "ascii",
		"\x1bP+q544e\x1b\\after":                              "after",
		"❯ \x1b[7m \x1b[27m":                                  "❯  ",
		"cut\x1b[38;5":                                        "cut",
		"cut\x1b":                                             "cut",
		"cut\x1b]0;never ends":                                "cut",
	}
	for in, want := range cases {
		if got := StripANSI(in); got != want {
			t.Errorf("StripANSI(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestTmuxNarrowCapturesKeepTheirReading is the dimensions and wrapping case:
// the same permission dialog at 120x36 and, after a live resize, at 64x30,
// where each harness re-wraps the option list.
func TestTmuxNarrowCapturesKeepTheirReading(t *testing.T) {
	wrapped := map[string]string{
		// The second option no longer fits on one row and continues on the next.
		"claude":  "   2. Yes, and switch to accept edits (auto-approve file edits\n      and common file commands)",
		"codex":   "  2. Yes, and don't ask again for commands that start with\n     `touch",
		"copilot": "in this directory  │\n│   (/private/tmp/amx-s9/work3)",
	}
	for harnessName, trap := range wrapped {
		t.Run(harnessName, func(t *testing.T) {
			wide := readTmuxFixture(t, harnessName+"_permission_prompt", ".txt")
			narrow := readTmuxFixture(t, harnessName+"_permission_prompt_narrow", ".txt")
			if !strings.Contains(narrow, trap) {
				t.Fatalf("the narrow capture no longer shows the wrapped option %q", trap)
			}
			if strings.Contains(wide, trap) {
				t.Fatal("the wide capture wraps the same way; the pair no longer differs in wrapping")
			}
			got, want := classifyFixture(t, harnessName, narrow), classifyFixture(t, harnessName, wide)
			if got.State != Blocked || got.State != want.State || got.Vetoes != want.Vetoes {
				t.Errorf("narrow reads %s/%s, wide reads %s/%s; want both blocked alike",
					got.State, got.Vetoes, want.State, want.Vetoes)
			}
			if got.Fingerprint == want.Fingerprint {
				t.Error("a re-wrapped screen kept its fingerprint")
			}
		})
	}
}

// TestTmuxFingerprintPairs tests the Layer B claim on tmux output: a live
// screen moves between two captures and an idle one does not.
func TestTmuxFingerprintPairs(t *testing.T) {
	pairs := []struct {
		t0, t1 string
		stable bool
	}{
		{"claude_working_t0", "claude_working_t1", false},
		{"claude_working_streaming_t0", "claude_working_streaming_t1", false},
		{"codex_working_t0", "codex_working_t1", false},
		{"copilot_working_t0", "copilot_working_t1", false},
		{"claude_idle_completed_t0", "claude_idle_completed_t1", true},
		{"codex_idle_completed_t0", "codex_idle_completed_t1", true},
		{"copilot_idle_completed_t0", "copilot_idle_completed_t1", true},
	}
	for _, pair := range pairs {
		a := Fingerprint(readTmuxFixture(t, pair.t0, ".txt"))
		b := Fingerprint(readTmuxFixture(t, pair.t1, ".txt"))
		if (a == b) != pair.stable {
			t.Errorf("%s/%s: fingerprints equal=%v, want stable=%v", pair.t0, pair.t1, a == b, pair.stable)
		}
	}
}

// TestTmuxClaudeSpinnerWithoutTimerIsWorking is the trap claude_working_no_timer
// was kept for: Claude 2.1.296 draws its spinner line with no elapsed timer at
// the start of a turn, over an empty composer.
func TestTmuxClaudeSpinnerWithoutTimerIsWorking(t *testing.T) {
	screen := readTmuxFixture(t, "claude_working_no_timer", ".txt")
	if !strings.Contains(screen, "\n✳ Moseying…\n") {
		t.Fatal("the capture no longer holds a spinner line without a timer")
	}
	embedded, _ := PatternsFor("claude")
	if !embedded.Match(claudeClauseComposerLine, screen, strings.ToLower(screen)) {
		t.Fatal("the capture no longer shows an empty composer under the spinner")
	}
	got := classifyFixture(t, "claude", screen)
	if got.State != Working || got.SettleableTerminal() {
		t.Errorf("a timer-less spinner read %s/%s", got.State, got.Vetoes)
	}
	if got.StatusLine != "✳ Moseying…" {
		t.Errorf("status line %q", got.StatusLine)
	}
}

// TestTmuxClaudeStreamingTurnIsOnlyHeldByItsFingerprint records a limit rather
// than a success. While Claude 2.1.296 streams text it draws no spinner line at
// all, so a single frame of a live turn reads exactly like a finished one. What
// keeps it from settling is the caller's stabilization gate: the frame changes
// between captures, and the weakest tier asks for the most stable samples.
func TestTmuxClaudeStreamingTurnIsOnlyHeldByItsFingerprint(t *testing.T) {
	t0 := readTmuxFixture(t, "claude_working_streaming_t0", ".txt")
	t1 := readTmuxFixture(t, "claude_working_streaming_t1", ".txt")
	for _, screen := range []string{t0, t1} {
		if strings.Contains(screen, "…") {
			t.Fatal("the capture now holds a spinner line; it no longer poses the trap")
		}
		if got := classifyFixture(t, "claude", screen); got.Tier != TierQuiescent {
			t.Errorf("a streaming frame read %s/%s; it must stay on the weakest tier", got.State, got.Tier)
		}
	}
	if !strings.Contains(t1, t0[strings.Index(t0, "⏺ Many Rooms"):strings.Index(t0, "⏺ Many Rooms")+40]) || len(t1) <= len(t0) {
		t.Fatal("t1 is not a later frame of the same streaming turn")
	}
	if Fingerprint(t0) == Fingerprint(t1) {
		t.Error("two frames of a streaming turn share a fingerprint")
	}
}

// TestTmuxExitedPaneScreenIsNotAFinishedTurn: tmux keeps a dead pane and its
// last screen. The screen is whatever the program left — here Claude's trust
// dialog above tmux's own notice — and reading it says nothing about a turn.
// The backend reports the exit; see probes.CheckScreen for the gate.
func TestTmuxExitedPaneScreenIsNotAFinishedTurn(t *testing.T) {
	screen := readTmuxFixture(t, "claude_exited_pane_dead", ".txt")
	if !strings.Contains(screen, "\nPane is dead (status 1,") {
		t.Fatal("the capture no longer shows tmux's dead-pane notice")
	}
	if got := classifyFixture(t, "claude", screen); got.SettleableTerminal() {
		t.Errorf("a dead pane's screen settled as %s/%s", got.State, got.Tier)
	}
}

// TestTmuxClaudeTitleCaptures holds the two pane titles to what they were
// taken to show: Claude 2.1.296 sets its title through tmux under the private
// configuration, names the turn there while it streams, and leaves that name
// in place once the turn is over. The title therefore says a turn was started,
// never that one is running; the screens beside it are what differ.
func TestTmuxClaudeTitleCaptures(t *testing.T) {
	working := readTmuxFixture(t, "claude_working_streaming_titled", ".title")
	resting := readTmuxFixture(t, "claude_idle_completed_titled", ".title")
	for name, title := range map[string]string{"working": working, "resting": resting} {
		body, marked := strings.CutPrefix(title, "✳ ")
		if !marked || !strings.HasSuffix(body, "\n") || strings.Count(title, "\n") != 1 {
			t.Fatalf("the %s title %q is not one line under Claude's marker", name, title)
		}
		if strings.TrimSpace(body) == "" || strings.TrimSpace(body) == "Claude Code" {
			t.Errorf("the %s title %q names no turn", name, title)
		}
	}
	if working != resting {
		t.Errorf("the title changed when the turn ended (%q, then %q); the pair no longer shows a title outliving its turn", working, resting)
	}
	streaming := readTmuxFixture(t, "claude_working_streaming_titled", ".txt")
	done := readTmuxFixture(t, "claude_idle_completed_titled", ".txt")
	if strings.Contains(streaming, "✻ Worked for") || !strings.Contains(done, "✻ Worked for") {
		t.Error("the pair is no longer a turn in flight and the same turn finished")
	}
	if Fingerprint(streaming) == Fingerprint(done) {
		t.Error("the streaming and finished screens share a fingerprint")
	}
}

// TestTmuxCodexClearedCapture is the trap codex_cleared was taken for.
// codex-cli 0.162.1 draws its header bare, so a cleared session's first line
// starts with ">" like a quoted transcript line, and it runs on the alternate
// screen, where a pane has no history: the screen and the screen with its
// history are the same bytes, and both must read as cleared.
func TestTmuxCodexClearedCapture(t *testing.T) {
	screen := readTmuxFixture(t, "codex_cleared", ".txt")
	all := readTmuxFixture(t, "codex_cleared", ".all.txt")
	if !strings.HasPrefix(screen, "\n  >_ OpenAI Codex (v0.162.1)\n") {
		t.Fatal("the capture no longer opens on a bare session header")
	}
	if !strings.Contains(screen, "Context 0% used") {
		t.Fatal("the capture no longer reports an empty context")
	}
	if all != screen {
		t.Error("the history capture differs from the screen; the pane was not on the alternate screen")
	}
	if !CodexScreenLooksCleared(screen) || !ClassifyCodexClearScreen(screen, all) {
		t.Errorf("a cleared session reads looks-cleared=%v cleared=%v",
			CodexScreenLooksCleared(screen), ClassifyCodexClearScreen(screen, all))
	}

	// Without the banner clause the header is a transcript line again.
	embedded, _ := PatternsFor("codex")
	clauses := codexClausesFor(embedded)
	if !clauses.transcriptMarker("  > quoted by the model") || clauses.transcriptMarker("  >_ OpenAI Codex (v0.162.1)") {
		t.Error("the banner exemption is not exactly the session header")
	}
	clauses.bannerRE = nil
	if clauses.screenLooksCleared(screen) {
		t.Error("the capture reads as cleared with no banner clause; it no longer poses the trap")
	}

	// No screen that still shows a turn, a prompt, or live work is cleared.
	// The bare composer of a new session is: it is the same screen.
	for _, fixture := range tmuxFixtures {
		if fixture.harness != "codex" {
			continue
		}
		text := readTmuxFixture(t, fixture.name, ".txt")
		want := fixture.name == "codex_cleared" || fixture.name == "codex_idle_bare_composer"
		if got := ClassifyCodexClearScreen(text, text); got != want {
			t.Errorf("%s: cleared = %v, want %v", fixture.name, got, want)
		}
	}
}

// backendParity pairs a Kitty capture with a tmux capture of the same kind of
// screen. Kitty captures are from earlier harness builds, so copy has moved;
// what must not move is the reading.
var backendParity = []struct {
	harness   string
	behaviour string
	kitty     string
	tmux      string
	state     State
}{
	{"claude", behaviourPrompt, "claude_permission_prompt.txt", "claude_trust_prompt", Blocked},
	{"claude", behaviourPermission, "claude_permission_prompt.txt", "claude_permission_prompt", Blocked},
	{"claude", behaviourBusy, "claude_working_pouncing_t0.txt", "claude_working_t0", Working},
	{"claude", behaviourCompletion, "claude_idle_completed.txt", "claude_idle_completed_t0", Terminal},
	{"claude", behaviourFailure, "claude_interrupted.txt", "claude_interrupted", Terminal},

	{"codex", behaviourPrompt, "codex_idle_completed_t0.txt", "codex_idle_completed_t0", Terminal},
	{"codex", behaviourPermission, "codex_prompt_command_approval.txt", "codex_permission_prompt", Blocked},
	{"codex", behaviourBusy, "codex_working.txt", "codex_working_t0", Working},
	{"codex", behaviourCompletion, "codex_idle_completed_t1.txt", "codex_idle_completed_t1", Terminal},
	{"codex", behaviourFailure, "codex_interrupted_no_background.txt", "codex_rejected_command", Terminal},

	{"copilot", behaviourPrompt, "copilot_trust_prompt.txt", "copilot_trust_prompt", Blocked},
	{"copilot", behaviourPermission, "copilot_permission_prompt.txt", "copilot_permission_prompt", Blocked},
	{"copilot", behaviourBusy, "copilot_working_t0.txt", "copilot_working_t0", Working},
	{"copilot", behaviourCompletion, "copilot_idle_completed_t0.txt", "copilot_idle_completed_t0", Unknown},
	{"copilot", behaviourFailure, "copilot_interrupted.txt", "copilot_interrupted", Unknown},
}

// TestBackendFixtureParity holds prompt, permission, busy, completion and
// failure detection to the same reading on both fixture sets, for all three
// harnesses.
//
// Two rows need saying out loud. Claude and Codex have no Kitty capture of a
// startup dialog, so their prompt rows pair the tmux capture with the nearest
// Kitty screen that waits on the user the same way: Claude's permission dialog
// (both blocked) and Codex's ready composer after a turn (both terminal). And
// Copilot's completion and failure rows are Unknown on both sides, because its
// screen is never evidence of a finished turn on any backend.
func TestBackendFixtureParity(t *testing.T) {
	covered := map[string]bool{}
	for _, row := range backendParity {
		t.Run(row.harness+"/"+row.behaviour, func(t *testing.T) {
			kitty := classifyFixture(t, row.harness, readScreenFixture(t, row.kitty))
			tmux := classifyFixture(t, row.harness, readTmuxFixture(t, row.tmux, ".txt"))
			if kitty.State != row.state || tmux.State != row.state {
				t.Errorf("kitty %s reads %s/%s, tmux %s reads %s/%s; want %s on both",
					row.kitty, kitty.State, kitty.Vetoes, row.tmux, tmux.State, tmux.Vetoes, row.state)
			}
			if kitty.SettleableTerminal() != tmux.SettleableTerminal() {
				t.Errorf("settleable: kitty %v, tmux %v", kitty.SettleableTerminal(), tmux.SettleableTerminal())
			}
			// The veto that decides the state must be the same one.
			for _, deciding := range []Veto{VetoBlockingPrompt, VetoLiveStatusLine} {
				if (kitty.Vetoes&deciding != 0) != (tmux.Vetoes&deciding != 0) {
					t.Errorf("%s: kitty %s, tmux %s", deciding, kitty.Vetoes, tmux.Vetoes)
				}
			}
		})
		covered[row.harness+"/"+row.behaviour] = true
	}
	// The cleared-screen check reads a screen and the screen with its history.
	// A Kitty window keeps the session's primary-screen scrollback and a tmux
	// pane on the alternate screen keeps none, so the second read differs in
	// kind; the verdict must not.
	for _, row := range []struct {
		name        string
		kitty, tmux string
		cleared     bool
	}{
		{"cleared", "codex_cleared.txt", "codex_cleared", true},
		{"completed", "codex_idle_completed_t0.txt", "codex_idle_completed_t0", false},
		{"permission", "codex_prompt_command_approval.txt", "codex_permission_prompt", false},
		{"busy", "codex_working.txt", "codex_working_t0", false},
	} {
		t.Run("codex/clear/"+row.name, func(t *testing.T) {
			kitty := readScreenFixture(t, row.kitty)
			tmux := readTmuxFixture(t, row.tmux, ".txt")
			tmuxAll := tmux
			if tmuxFixtureExtras[row.tmux] == ".all.txt" {
				tmuxAll = readTmuxFixture(t, row.tmux, ".all.txt")
			}
			if got := ClassifyCodexClearScreen(kitty, kitty); got != row.cleared {
				t.Errorf("kitty %s: cleared = %v, want %v", row.kitty, got, row.cleared)
			}
			if got := ClassifyCodexClearScreen(tmux, tmuxAll); got != row.cleared {
				t.Errorf("tmux %s: cleared = %v, want %v", row.tmux, got, row.cleared)
			}
		})
	}

	var missing []string
	for harnessName := range tmuxFixtureVersions {
		for _, behaviour := range []string{
			behaviourPrompt, behaviourPermission, behaviourBusy, behaviourCompletion, behaviourFailure,
		} {
			if !covered[harnessName+"/"+behaviour] {
				missing = append(missing, harnessName+"/"+behaviour)
			}
		}
	}
	sort.Strings(missing)
	if len(missing) != 0 {
		t.Errorf("no parity row for %v", missing)
	}
}

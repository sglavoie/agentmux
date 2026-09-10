package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/internal/transcript"
)

func TestRenderHelpNoHeightShowsAll(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.mode = ModeHelp
	m.height = 0

	out := stripAnsi(renderHelp(m))

	for _, header := range []string{"Dashboard", "Navigate", "Act on row", "Launch / open", "Filter", "App", "Past Sessions", "Plans", "Worktrees"} {
		if !strings.Contains(out, header) {
			t.Errorf("expected section header %q in help output, got:\n%s", header, out)
		}
	}
	if !strings.Contains(out, "footer overflow") {
		t.Errorf("expected footer overflow line in help output, got:\n%s", out)
	}
}

// TestDashboardHelpDocumentsEveryFooterBinding is the parity guard behind the
// `? more` marker. The capped dashboard footer omits its lower bands on a narrow
// terminal and tells the operator to press `?`; if a binding were declared in the
// palette but missing from the generated section, that promise would be a dead
// end. Both directions are asserted — no binding missing from help, and no
// generated line the overlay does not actually render.
func TestDashboardHelpDocumentsEveryFooterBinding(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.mode = ModeHelp
	m.height = 0

	out := stripAnsi(renderHelp(m))
	lines := helpDashboardSection()

	bindings := listFooterBindings()
	if len(lines) != len(bindings) {
		t.Errorf("expected one generated line per binding (%d), got %d: %v", len(bindings), len(lines), lines)
	}
	for _, e := range bindings {
		line, ok := helpPaletteLineFor(lines, e.key)
		if !ok {
			t.Errorf("palette binding %q (%s) has no generated Dashboard line: %v", e.key, e.label, lines)
			continue
		}
		if !strings.Contains(line, e.label) {
			t.Errorf("generated line %q does not carry the palette label %q", line, e.label)
		}
		if !strings.Contains(out, line) {
			t.Errorf("overlay does not render the generated line %q, got:\n%s", line, out)
		}
	}
}

// TestDashboardHelpOmitsNonBindingCandidates asserts the two metadata
// candidates stay out of the keymap. The position fragment and the state gloss
// compete with the bindings for footer space, but they bind nothing: a keymap
// row for either would advertise a keystroke that does not exist.
func TestDashboardHelpOmitsNonBindingCandidates(t *testing.T) {
	metadata := 0
	for _, c := range listFooterPalette() {
		if c.textFor == nil {
			continue
		}
		metadata++
		if c.key != "" {
			t.Errorf("metadata candidate declares key %q; it would leak into generated help", c.key)
		}
	}
	if metadata == 0 {
		t.Fatal("the palette declares no metadata candidates; this guard is inert")
	}
	if got, want := len(listFooterBindings()), len(listFooterPalette())-metadata; got != want {
		t.Errorf("listFooterBindings() returned %d entries, want %d bindings", got, want)
	}
}

func TestRenderHelpReflectsDashboardRemap(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.mode = ModeHelp
	out := stripAnsi(renderHelp(m))

	for _, frag := range []string{
		":         open command palette",
		"x         clear session icon",
		"X         close session (confirm)",
		"C         launch new session with profile selection",
		"f         cycle profile (dashboard live / launcher default)",
		"o         open selected launcher directories (highlighted fallback)",
		"e         peek scrollback (active rows only)",
	} {
		if !strings.Contains(out, frag) {
			t.Errorf("expected help output to contain %q, got:\n%s", frag, out)
		}
	}
	for _, stale := range []string{
		"x         close session (confirm)",
		"C         clear session icon",
		"F         run an agent-session-loop plan",
		"t         tail active agent-session-loop run",
		// Past Sessions / Plans bindings now come from the footer palettes;
		// the static duplicates must not come back.
		"i         copy selected past session ID",
		"Y         copy current transcript/plan path in search/view modes",
		"cycle plan sort order",
		"open the plan in $EDITOR",
		"handoff selected past session to another profile",
		"also work inside the transcript viewer",
	} {
		if strings.Contains(out, stale) {
			t.Errorf("expected stale help hint %q to be absent, got:\n%s", stale, out)
		}
	}
}

// helpPaletteLineFor returns the generated help line for key, identified by the
// padded key column helpPaletteLines renders.
func helpPaletteLineFor(lines []string, key string) (string, bool) {
	prefix := fmt.Sprintf("  %-10s", key)
	for _, line := range lines {
		if strings.HasPrefix(line, prefix) {
			return line, true
		}
	}
	return "", false
}

// TestHelpDocumentsEveryPaletteKey verifies the generated Past Sessions and
// Plans sections document every key of the four mode palettes exactly once.
// Narrow terminals drop the low-priority footer hints, so the overlay is the
// only place those keymaps are guaranteed to be complete.
func TestHelpDocumentsEveryPaletteKey(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.mode = ModeHelp
	m.height = 0

	out := stripAnsi(renderHelp(m))

	for _, sec := range helpPaletteSections {
		var keys []string
		seen := make(map[string]bool)
		for _, palette := range sec.palettes {
			for _, e := range palette.entries {
				if seen[e.key] {
					continue
				}
				seen[e.key] = true
				keys = append(keys, e.key)
			}
		}

		lines := helpPaletteLines(sec.palettes)
		if len(lines) != len(keys) {
			t.Errorf("%s: expected one line per distinct key (%d), got %d: %v", sec.name, len(keys), len(lines), lines)
		}
		for _, key := range keys {
			line, ok := helpPaletteLineFor(lines, key)
			if !ok {
				t.Errorf("%s: expected a generated line for key %q, got %v", sec.name, key, lines)
				continue
			}
			if !strings.Contains(out, line) {
				t.Errorf("%s: expected overlay to render %q, got:\n%s", sec.name, line, out)
			}
		}
	}
}

// TestHelpPreservesModeSpecificMeaning pins F-002: when a section's search and
// viewer palettes label the same key differently, the generated help must keep
// both meanings and name the scope each belongs to. Dropping either one would
// tell the user the viewer key does something it does not.
func TestHelpPreservesModeSpecificMeaning(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.mode = ModeHelp
	m.height = 0

	out := stripAnsi(renderHelp(m))

	divergent := 0
	for _, sec := range helpPaletteSections {
		declared := make(map[string][]helpKeyMeaning)
		var keys []string
		for _, palette := range sec.palettes {
			for _, e := range palette.entries {
				if _, ok := declared[e.key]; !ok {
					keys = append(keys, e.key)
				}
				declared[e.key] = append(declared[e.key], helpKeyMeaning{palette.scope, e.label})
			}
		}

		lines := helpPaletteLines(sec.palettes)
		for _, key := range keys {
			distinct := make(map[string]bool, len(declared[key]))
			var meanings []string
			for _, d := range declared[key] {
				distinct[d.label] = true
				meanings = append(meanings, fmt.Sprintf("%s (%s)", d.label, d.scope))
			}
			if len(distinct) < 2 {
				continue
			}
			divergent++

			line, ok := helpPaletteLineFor(lines, key)
			if !ok {
				t.Errorf("%s: expected a generated line for key %q, got %v", sec.name, key, lines)
				continue
			}
			for _, meaning := range meanings {
				if !strings.Contains(line, meaning) {
					t.Errorf("%s: key %q line %q dropped the meaning %q", sec.name, key, line, meaning)
				}
			}
			if !strings.Contains(out, line) {
				t.Errorf("%s: expected overlay to render %q, got:\n%s", sec.name, line, out)
			}
		}
	}
	if divergent == 0 {
		t.Fatal("expected at least one key whose search and viewer labels differ; the guard is inert otherwise")
	}

	// The three meanings F-002 reported as suppressed by first-label-wins.
	for _, want := range []string{
		"enter     view (search) · view message (viewer)",
		"/         filter (search) · search (viewer)",
		"j/k       up/down (search) · scroll (viewer)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected overlay to render %q, got:\n%s", want, out)
		}
	}
}

// TestHelpScopesModeExclusiveKeys pins F-002: a key only one palette of a
// combined section declares must name the scope that declares it, or the
// overlay advertises a command that silently does nothing in the sibling mode.
// Keys every palette declares with the same label must stay bare so scoping the
// exclusives does not turn the common case into noise.
func TestHelpScopesModeExclusiveKeys(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.mode = ModeHelp
	m.height = 0

	out := stripAnsi(renderHelp(m))

	exclusive, shared := 0, 0
	for _, sec := range helpPaletteSections {
		declared := make(map[string][]helpKeyMeaning)
		var keys []string
		for _, palette := range sec.palettes {
			for _, e := range palette.entries {
				if _, ok := declared[e.key]; !ok {
					keys = append(keys, e.key)
				}
				declared[e.key] = append(declared[e.key], helpKeyMeaning{palette.scope, e.label})
			}
		}

		lines := helpPaletteLines(sec.palettes)
		for _, key := range keys {
			line, ok := helpPaletteLineFor(lines, key)
			if !ok {
				t.Errorf("%s: expected a generated line for key %q, got %v", sec.name, key, lines)
				continue
			}
			if !strings.Contains(out, line) {
				t.Errorf("%s: expected overlay to render %q, got:\n%s", sec.name, line, out)
			}

			if len(declared[key]) < len(sec.palettes) {
				exclusive++
				for _, d := range declared[key] {
					want := fmt.Sprintf("%s (%s)", d.label, d.scope)
					if !strings.Contains(line, want) {
						t.Errorf("%s: key %q is declared only by %s but line %q does not scope it as %q", sec.name, key, d.scope, line, want)
					}
				}
				continue
			}

			distinct := make(map[string]bool, len(declared[key]))
			for _, d := range declared[key] {
				distinct[d.label] = true
			}
			if len(distinct) > 1 {
				continue
			}
			shared++
			if want := fmt.Sprintf("  %-10s%s", key, declared[key][0].label); line != want {
				t.Errorf("%s: key %q means the same thing in every mode; want the concise %q, got %q", sec.name, key, want, line)
			}
		}
	}
	if exclusive == 0 || shared == 0 {
		t.Fatalf("expected both mode-exclusive (%d) and identically-shared (%d) keys; the guard is inert otherwise", exclusive, shared)
	}

	// The four mode-exclusive keys F-002 named as rendered without a scope.
	for _, want := range []string{
		"d         delete (search)",
		"o         sort (search)",
		"n/N       match (viewer)",
		"y         copy (viewer)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected overlay to render %q, got:\n%s", want, out)
		}
	}
}

// contextualHelpModel seeds a Past Sessions / Plans model with the search and
// viewer state a help detour must leave untouched.
func contextualHelpModel(mode Mode) Model {
	meta := viewerActionMeta()
	plan := plans.PlanMeta{Name: "plan-a", DirPath: "/tmp/plan-a"}
	return Model{
		mode:             mode,
		width:            80,
		height:           40,
		cfg:              viewerActionConfig(),
		sessionQuery:     "worktree",
		sessionResults:   []transcript.Result{{Meta: meta}},
		sessionCursor:    0,
		viewerSession:    &meta,
		viewerMessages:   []transcript.Message{{Role: "user", Text: "hello"}, {Role: "assistant", Text: "hi"}},
		viewerCursor:     1,
		planQuery:        "review",
		planResults:      []plans.Result{{Meta: plan}},
		viewerPlan:       &plan,
		planViewerLines:  []string{"--- plan.md ---", "body"},
		planViewerCursor: 1,
	}
}

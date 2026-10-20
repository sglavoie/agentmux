package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/tui/layout"
	"github.com/sglavoie/agentmux/tui/sessionactions"
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
	if !strings.Contains(out, "hidden footer keys") {
		t.Errorf("expected the ? more footnote in help output, got:\n%s", out)
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
	// One line per KEY, not per binding. Tab is declared twice — `switch pane`
	// beside an inspector, `focus next` without one — and help has no terminal
	// size to decide between them, so it carries both meanings on the one line
	// the key gets.
	keys := map[string]bool{}
	for _, e := range bindings {
		keys[e.key] = true
	}
	if len(lines) != len(keys) {
		t.Errorf("expected one generated line per distinct key (%d), got %d: %v", len(keys), len(lines), lines)
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
		// The per-row commands moved behind `a`, and help says so in the
		// spelling the operator actually types: "a, then <letter>".
		"a, then x clear status",
		"a, then X end session",
		"a, then C launch another session",
		"a, then e peek scrollback",
		// The launcher kept its own direct keymap; only the dashboard's shrank.
		"f         cycle the launch profile (in the launcher)",
		":         then \"Show Projects View\" to group by project",
		"o         open selected launcher directories (highlighted fallback)",
	} {
		if !strings.Contains(out, frag) {
			t.Errorf("expected help output to contain %q, got:\n%s", frag, out)
		}
	}
	for _, stale := range []string{
		// The retired dashboard spellings. Each names a key the dispatcher now
		// drops, so help listing one would be a dead end rather than a typo.
		"x         clear session icon",
		// `f` on the dashboard is retired, and the palette has no command
		// titled "Toggle List View" for `:` to find.
		"cycle profile (dashboard live",
		"Toggle List View",
		"X         end session (follows confirmation preference)",
		"C         launch new session with profile selection",
		"e         peek scrollback (active rows only)",
		"x         end session (follows confirmation preference)",
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
		"/         filter (search) · refine query (viewer)",
		"j/k       up/down",
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

// TestHelpKeymapPaintsItsKeysLikeKeys pins the styling the rest of this file
// cannot see: every assertion around it strips ANSI first, so the keymap could
// go entirely uniform without one of them noticing.
//
// The overlay is the one screen whose whole subject is which key does what, and
// a reader arrives already knowing what they want to do — they are scanning for
// a glyph, not reading prose. It was the last surface in the dashboard where a
// key was not the footer's teal.
func TestHelpKeymapPaintsItsKeysLikeKeys(t *testing.T) {
	lines := helpContentLines()
	joined := strings.Join(lines, "\n")

	// The padding is inside the styled span because the column belongs to the
	// key, not to the gap after it; asserting on the padded spelling is what
	// makes this a claim about the column rather than about a substring.
	for _, key := range []string{"j/k", "enter", "?", "esc", "shift+tab"} {
		if !strings.Contains(joined, styleFooterKey.Render(fmt.Sprintf("%-*s", helpKeyColumnWidth, key))) {
			t.Errorf("key %q is not painted as a key anywhere in the keymap", key)
		}
	}
	if !strings.Contains(joined, helpKeyBodyLine(helpFooterMoreLine)) {
		t.Error("the overflow footnote is not painted like the keys it points at")
	}

	// Two kinds of line deliberately keep the quiet treatment. A parenthetical
	// is an aside about the keys above it, and the icon column is a glyph the
	// daemon paints rather than one anybody presses; styling either like a key
	// would advertise a binding that does not exist.
	const aside = "  (n/N move the cursor only — Enter is what opens a terminal)"
	if got := helpKeyBodyLine(aside); got != styleDim.Render(aside) {
		t.Errorf("a parenthetical aside grew a key column: %q", got)
	}
	// A wrapped aside carries on past the opening parenthesis, so the third and
	// later lines of one start in the key column without being a key. They are
	// spelled with a deeper indent, and that is what the renderer reads.
	const continuation = "   are palette entries — search the palette by name)"
	if got := helpKeyBodyLine(continuation); got != styleDim.Render(continuation) {
		t.Errorf("a continuation line grew a key column: %q", got)
	}
	for _, line := range helpStateIconSection() {
		if !strings.Contains(joined, styleDim.Render(line)) {
			t.Errorf("icon reference line %q is not rendered dim in the overlay", line)
		}
	}

	// No key is wider than the column that holds it. The split is positional, so
	// a binding spelled past the tenth cell would be cut in half and the half
	// left behind would be painted as prose — a silent corruption a stripped
	// assertion reads straight through. Checked the way the renderer splits, on
	// display cells rather than bytes, so a wide glyph counts as what it prints.
	for _, line := range lines {
		plain := stripAnsi(line)
		if !strings.HasPrefix(plain, "  ") || strings.HasPrefix(plain, "  (") || strings.HasPrefix(plain, "   ") {
			continue
		}
		body := plain[2:]
		if strings.TrimSpace(body) == "" || layout.Width(body) <= helpKeyColumnWidth {
			continue
		}
		if column := layout.Truncate(body, helpKeyColumnWidth, ""); !strings.HasSuffix(column, " ") {
			t.Errorf("the key column of %q runs past %d cells and is cut at %q",
				plain, helpKeyColumnWidth, column)
		}
	}
}

// TestHelpActOnRowFollowsTheSessionActionTable pins the Act on row section to
// sessionactions.MenuDefinitions: every menu mnemonic is listed with the
// label and description the menu itself draws, in menu order, and Enter is
// described for both backends rather than as a Kitty tab focus.
func TestHelpActOnRowFollowsTheSessionActionTable(t *testing.T) {
	var section []string
	for _, sec := range helpSections {
		if sec.name == "Act on row" {
			section = sec.lines
		}
	}
	if section == nil {
		t.Fatal("help has no Act on row section")
	}
	joined := strings.Join(section, "\n")
	last := -1
	for _, def := range sessionactions.MenuDefinitions() {
		if def.Mnemonic.Key == keyEnter {
			want := "  enter     " + strings.ToLower(def.MenuLabel) + " (attach on tmux, focus tab on Kitty)"
			if !strings.Contains(joined, want) {
				t.Errorf("Act on row lacks %q:\n%s", want, joined)
			}
			continue
		}
		want := "  a, then " + def.Mnemonic.Display + " " + strings.ToLower(def.MenuLabel) + " — " + def.MenuDescription
		at := strings.Index(joined, want)
		if at < 0 {
			t.Errorf("Act on row lacks the menu's %q entry; want line %q", def.Mnemonic.Key, want)
			continue
		}
		if at < last {
			t.Errorf("Act on row lists %q out of menu order", def.Mnemonic.Key)
		}
		last = at
	}
	for _, sec := range helpSections {
		for _, line := range sec.lines {
			if sec.name != "Act on row" && strings.HasPrefix(line, "  a, then ") {
				t.Errorf("%s restates a menu mnemonic outside Act on row: %q", sec.name, line)
			}
		}
	}
	if strings.Contains(joined, "focus the session's tab") {
		t.Error("Act on row still describes Enter as a Kitty-only tab focus")
	}
}

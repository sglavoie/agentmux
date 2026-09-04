package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/layout"
)

// TestListFooterPaletteOrder asserts that the palette is rendered in its
// declaration order regardless of the cursor row's state. The test
// reconstructs the expected fragment positions and verifies each entry's
// `key label` substring appears in order across the stripped command rows.
func TestListFooterPaletteOrder(t *testing.T) {
	cases := []struct {
		name string
		m    Model
	}{
		{
			name: "empty list",
			m:    Model{},
		},
		{
			name: "attention row",
			m:    withListRows(Model{}, session.RunningSession{Base: "a", State: "key", Account: "w"}),
		},
		{
			name: "active row",
			m:    withListRows(Model{}, session.RunningSession{Base: "a", State: "bolt", Account: "w"}),
		},
		{
			name: "idle row",
			m:    withListRows(Model{}, session.RunningSession{Base: "a", State: "", Account: "w"}),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := listFooterCommandBlock(tc.m)
			prev := -1
			for _, e := range listFooterPalette {
				frag := e.key + " " + e.label
				idx := strings.Index(out, frag)
				if idx < 0 {
					t.Errorf("expected fragment %q in footer; got %q", frag, out)
					continue
				}
				if idx <= prev {
					t.Errorf("fragment %q out of order (idx=%d, prev=%d) in %q", frag, idx, prev, out)
				}
				prev = idx
			}
		})
	}
}

// TestListFooterActionsReflow asserts row-targeted and global app commands
// share one wrapping flow. At this width the first global action must use the
// space remaining after the final row action instead of starting a new block.
func TestListFooterActionsReflow(t *testing.T) {
	const width = 110
	out := stripAnsi(listFooterActions(withListRows(Model{width: width}, session.RunningSession{State: "bolt"})))

	if strings.Contains(out, "▏") {
		t.Errorf("footer should not render section bars; got %q", out)
	}

	lines := strings.Split(out, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected two reflowed action rows, got %d in %q", len(lines), out)
	}
	if !strings.Contains(lines[1], "O open in editor · f profile") {
		t.Fatalf("expected global actions to continue after final row action; got %q", lines[1])
	}
	for _, frag := range []string{
		"e peek", "s project sessions", "O open in editor",
		"f profile", "l launch", ": command palette", "S sessions", "P plans", "R resort",
	} {
		if !strings.Contains(out, frag) {
			t.Errorf("expected action %q in reflowed footer; got %q", frag, out)
		}
	}
	for _, stale := range []string{"F run plan", "t tail"} {
		if strings.Contains(out, stale) {
			t.Errorf("stale loop command %q should not be advertised; got %q", stale, out)
		}
	}
}

func TestListFooterHasNoSpacerAndZebraRowsSpanViewport(t *testing.T) {
	const width = 110
	m := withListRows(Model{mode: ModeList, width: width}, session.RunningSession{State: "bolt"})

	rendered := renderFooter(m)
	rawLines := strings.Split(rendered, "\n")
	plainLines := strings.Split(stripAnsi(rendered), "\n")
	if len(rawLines) < 3 {
		t.Fatalf("expected wrapped actions followed by navigation, got %d rows:\n%s", len(rawLines), rendered)
	}
	for i, line := range plainLines {
		if strings.TrimSpace(line) == "" {
			t.Fatalf("unexpected blank footer row %d in:\n%s", i, stripAnsi(rendered))
		}
	}
	if !strings.Contains(plainLines[1], "O open in editor · f profile") {
		t.Fatalf("expected actions to reflow across the former section boundary, got %q", plainLines[1])
	}
	if !strings.Contains(plainLines[2], "j/k up/down") {
		t.Fatalf("expected navigation immediately after actions, got %q", plainLines[2])
	}

	assertZebraFooterRows(t, rendered, width)
}

func TestRootFootersHaveZebraRows(t *testing.T) {
	cases := []struct {
		name string
		m    Model
	}{
		{name: "dashboard list", m: Model{mode: ModeList, width: 72}},
		{name: "dashboard search", m: Model{mode: ModeSearch, width: 72}},
		{name: "confirmation", m: Model{mode: ModeConfirm, width: 72}},
		{name: "session launcher", m: Model{mode: ModePicker, width: 72}},
		{name: "config picker", m: Model{mode: ModeConfigPicker, width: 72}},
		{name: "help", m: Model{mode: ModeHelp, width: 72}},
		{name: "actions", m: Model{mode: ModeActions, width: 72}},
		{name: "command palette", m: Model{mode: ModeCommandPalette, width: 72}},
		{name: "session browser", m: Model{mode: ModeSessionSearch, width: 72}},
		{name: "transcript viewer", m: Model{mode: ModeSessionView, width: 72}},
		{name: "delete choice", m: Model{mode: ModeDeleteChoice, width: 72}},
		{name: "chip picker", m: Model{mode: ModeChipPicker, width: 72}},
		{name: "message reader", m: Model{mode: ModeMessageDetail, width: 72}},
		{name: "plan browser", m: Model{mode: ModePlanSearch, width: 72}},
		{name: "plan viewer", m: Model{mode: ModePlanView, width: 72}},
		{name: "jump chord", m: Model{mode: ModeList, width: 72, pendingJumpDir: 1}},
		{
			name: "active filter",
			m: Model{
				mode:         ModeList,
				width:        72,
				activeFilter: dashboard.Filter{Substrings: []string{"api"}},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertZebraFooterRows(t, renderFooter(tc.m), tc.m.width)
		})
	}
}

func TestFooterZebraExcludesViewsWithoutRootFooter(t *testing.T) {
	cases := []struct {
		name string
		m    Model
	}{
		{name: "detail overlay", m: Model{mode: ModeDetail, width: 72}},
		{name: "dashboard preview", m: Model{mode: ModeList, width: 72, previewEnabled: true}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := renderFooter(tc.m); got != "" {
				t.Errorf("renderFooter() = %q, want no root footer", got)
			}
		})
	}
}

func assertZebraFooterRows(t *testing.T, rendered string, width int) {
	t.Helper()
	if rendered == "" {
		t.Fatal("expected a root footer")
	}

	background := footerStripeBackgroundSequence()
	for i, line := range strings.Split(rendered, "\n") {
		if i%2 == 0 {
			if !strings.HasPrefix(line, background) {
				t.Errorf("row %d should start with zebra background", i)
			}
			if got := layout.StyledWidth(line); got != width {
				t.Errorf("striped row %d width = %d, want full viewport width %d", i, got, width)
			}
			if !strings.Contains(line, ansi.ResetStyle+background) {
				t.Errorf("striped row %d should reapply background after nested style resets", i)
			}
			continue
		}
		if strings.Contains(line, background) {
			t.Errorf("row %d should retain the terminal's default background", i)
		}
	}
}

func listFooterCommandBlock(m Model) string {
	return stripAnsi(listFooterActions(m))
}

// TestListFooterKeyHintsAlwaysPresent locks in the discoverability
// requirement from the brief: `e` (peek), `C` (launch), `x` (clear), and
// `X` (close) must appear in the footer in every state group, even when dimmed.
func TestListFooterKeyHintsAlwaysPresent(t *testing.T) {
	cases := []struct {
		name string
		m    Model
	}{
		{
			name: "empty list",
			m:    Model{},
		},
		{
			name: "attention",
			m:    withListRows(Model{}, session.RunningSession{State: "key"}),
		},
		{
			name: "active",
			m:    withListRows(Model{}, session.RunningSession{State: "bolt"}),
		},
		{
			name: "idle",
			m:    withListRows(Model{}, session.RunningSession{State: ""}),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := stripAnsi(listFooterActions(tc.m))
			if !strings.Contains(out, "e peek") {
				t.Errorf("expected `e peek` in footer; got %q", out)
			}
			for _, frag := range []string{"C launch", "x clear", "X close"} {
				if !strings.Contains(out, frag) {
					t.Errorf("expected %q in footer; got %q", frag, out)
				}
			}
			for _, stale := range []string{"F run plan", "t tail"} {
				if strings.Contains(out, stale) {
					t.Errorf("stale loop command %q should not be advertised; got %q", stale, out)
				}
			}
		})
	}
}

func TestViewerFootersAdvertiseCopy(t *testing.T) {
	cases := []struct {
		name  string
		mode  Mode
		wants []string
	}{
		{name: "session search", mode: ModeSessionSearch, wants: []string{"i ID", "Y path"}},
		{name: "session viewer", mode: ModeSessionView, wants: []string{"y copy", "Y path"}},
		{name: "plan search", mode: ModePlanSearch, wants: []string{"Y path"}},
		{name: "plan viewer", mode: ModePlanView, wants: []string{"y copy", "Y path"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := stripAnsi(renderFooter(Model{mode: tc.mode}))
			for _, want := range tc.wants {
				if !strings.Contains(out, want) {
					t.Fatalf("footer = %q, want %q hint", out, want)
				}
			}
		})
	}
}

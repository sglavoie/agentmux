package tui

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
)

// chipPickerFixture builds a Model wired for ModeChipPicker rendering. The
// height is generous so chipPickerVisibleHeight returns the full 12-row cap
// and no overflow indicators are drawn (those are exercised by the scroll
// tests).
func chipPickerFixture(kind chipPickerKind, opts []string, cursor int) Model {
	return Model{
		mode:              ModeChipPicker,
		width:             100,
		height:            40,
		chipPickerKind:    kind,
		chipPickerOptions: opts,
		chipPickerCursor:  cursor,
	}
}

// TestRenderChipPickerHeaderByKind locks in the header copy for each chip
// kind. The renderer chooses the string from m.chipPickerKind alone.
func TestRenderChipPickerHeaderByKind(t *testing.T) {
	cases := []struct {
		kind chipPickerKind
		want string
	}{
		{chipPickerHarness, "Choose harness"},
		{chipPickerProfile, "Choose profile"},
		{chipPickerProject, "Choose project"},
	}
	for _, tc := range cases {
		m := chipPickerFixture(tc.kind, []string{""}, 0)
		out := stripAnsi(renderChipPicker(m))
		if !strings.Contains(out, tc.want) {
			t.Errorf("kind=%d: expected header %q in output; got %q", tc.kind, tc.want, out)
		}
	}
}

// TestRenderChipPickerAnyFirstWhenNoFilter verifies the "(any)" row sits at
// the top of the list and carries the cursor highlight when no filter is set.
func TestRenderChipPickerAnyFirstWhenNoFilter(t *testing.T) {
	m := chipPickerFixture(chipPickerHarness, []string{"", "claude", "codex"}, 0)
	raw := renderChipPicker(m)
	out := stripAnsi(raw)

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	// Layout: header, (any), claude, codex.
	if len(lines) < 4 {
		t.Fatalf("expected at least 4 lines; got %d (%q)", len(lines), out)
	}
	if !strings.Contains(lines[1], "(any)") {
		t.Errorf("expected (any) on row 1; got %q", lines[1])
	}
	// With no filter set, currentChipValue == "" so the (any) row also gets
	// the (current) annotation. We only assert position + cursor styling.
	if !rowIsStyled(raw, "(any)") {
		t.Errorf("expected (any) row to carry cursor styling; raw=%q", raw)
	}
}

// TestRenderChipPickerCurrentAnnotation verifies the active filter value is
// annotated with "(current)" so users can see the live selection without
// counting rows.
func TestRenderChipPickerCurrentAnnotation(t *testing.T) {
	m := chipPickerFixture(chipPickerHarness, []string{"", "claude", "codex"}, 1)
	m.filterHarness = []string{"claude"}
	out := stripAnsi(renderChipPicker(m))

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	var claudeLine string
	for _, l := range lines {
		if strings.Contains(l, "claude") {
			claudeLine = l
			break
		}
	}
	if claudeLine == "" {
		t.Fatalf("claude row not rendered; got %q", out)
	}
	if !strings.Contains(claudeLine, "(current)") {
		t.Errorf("expected (current) annotation on claude row; got %q", claudeLine)
	}
}

// TestRenderChipPickerEmptyRealOptions checks the degenerate case where the
// real option list is empty (so options == [""]). The overlay must still
// render the (any) row without panicking.
func TestRenderChipPickerEmptyRealOptions(t *testing.T) {
	m := chipPickerFixture(chipPickerProfile, []string{""}, 0)
	out := stripAnsi(renderChipPicker(m))
	if !strings.Contains(out, "(any)") {
		t.Errorf("expected (any) row in single-option output; got %q", out)
	}
	if !strings.Contains(out, "Choose profile") {
		t.Errorf("expected header in single-option output; got %q", out)
	}
}

// TestRenderChipPickerCursorRowStyled verifies the cursor row is rendered
// with ANSI styling (Bold+Reverse via styleSelected) while sibling rows are
// plain. Without a textual marker, the styling itself is the signal.
func TestRenderChipPickerCursorRowStyled(t *testing.T) {
	m := chipPickerFixture(chipPickerProject, []string{"", "alpha", "beta", "gamma"}, 2)
	raw := renderChipPicker(m)

	if !rowIsStyled(raw, "beta") {
		t.Errorf("expected cursor row 'beta' to carry ANSI styling; raw=%q", raw)
	}
	if rowIsStyled(raw, "gamma") {
		t.Errorf("non-cursor row 'gamma' should be unstyled; raw=%q", raw)
	}
	if rowIsStyled(raw, "alpha") {
		t.Errorf("non-cursor row 'alpha' should be unstyled; raw=%q", raw)
	}
}

// TestEnterChipPickerGhostFilterFallsBackToAny exercises the config-reload
// edge case: the active filter value is no longer in the option list, so the
// cursor must land on the (any) row rather than running off-window.
func TestEnterChipPickerGhostFilterFallsBackToAny(t *testing.T) {
	m := Model{
		mode:          ModeSessionSearch,
		width:         100,
		height:        40,
		filterProfile: []string{"ghost-profile"},
		cfg: &config.Config{
			Profiles: []config.Account{{Name: "work"}},
		},
	}
	// "ghost-profile" is not in cfg.Profiles, so enterChipPicker must fall
	// back to cursor 0 rather than panicking or running off-window.
	got, _ := m.enterChipPicker(chipPickerProfile)
	next := got.(Model)
	if next.chipPickerCursor != 0 {
		t.Errorf("expected cursor 0 when filter value is absent; got %d", next.chipPickerCursor)
	}
	if len(next.chipPickerOptions) == 0 || next.chipPickerOptions[0] != "" {
		t.Errorf("expected leading (any) row; got options=%#v", next.chipPickerOptions)
	}
}

// rowIsStyled reports whether the line containing label in raw also carries
// the ANSI escape introducer (\x1b[) emitted by styleSelected. The renderer
// only wraps the cursor row in styleSelected, so this is a precise check.
func rowIsStyled(raw, label string) bool {
	for _, line := range strings.Split(raw, "\n") {
		plain := stripAnsi(line)
		if strings.Contains(plain, label) {
			return strings.Contains(line, "\x1b[")
		}
	}
	return false
}

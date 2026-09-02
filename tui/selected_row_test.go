package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/launch"
	"github.com/sglavoie/agentmux/tui/layout"
)

func TestRenderSelectedRowNormalizesNestedANSI(t *testing.T) {
	plain := "  🦊 profile  codex  /tmp/项目  (current)"
	nested := lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555")).Render("  🦊 profile") +
		styleDim.Render("  codex  /tmp/项目") +
		lipgloss.NewStyle().Foreground(lipgloss.Color("#4ec9b0")).Render("  (current)")

	got := renderSelectedRow(nested)
	want := styleSelected.Render(plain)
	if got != want {
		t.Fatalf("selected row retained nested ANSI styling:\n got %q\nwant %q", got, want)
	}
	if visible := stripAnsi(got); visible != plain {
		t.Fatalf("selected visible content = %q, want %q", visible, plain)
	}
	if gotWidth, wantWidth := layout.StyledWidth(got), layout.Width(plain); gotWidth != wantWidth {
		t.Fatalf("selected width = %d, want %d", gotWidth, wantWidth)
	}
	if sequences := strings.Count(got, "\x1b["); sequences != 2 {
		t.Fatalf("selected row has %d ANSI sequences, want one opening span and one reset: %q", sequences, got)
	}
}

func TestConfigPickerCursorMoveRestoresNormalRow(t *testing.T) {
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2},
		HarnessEmojis: map[string]string{"claude": "🦊", "codex": "🤖"},
		HarnessColors: map[string]string{"claude": "blue", "codex": "green"},
		Accounts: []config.Account{
			{Name: "claude-production", Harness: "claude", HomeDir: "/profiles/claude", Color: "blue"},
			{Name: "codex-sandbox", Harness: "codex", HomeDir: "/profiles/codex", Color: "green"},
		},
	}
	client := kitty.NewClientWithSocket(kitty.NewMockRunner(nil), "")
	m := NewModel(cfg, client)
	m.mode = ModeConfigPicker
	m.targetSession = session.RunningSession{Base: "agentmux", Project: "agentmux", Harness: "claude"}
	m.configPickerAccounts = cfg.Accounts
	m.configPickerCurrent = "codex-sandbox"
	m.configPickerCursor = 0
	m.configPickerPurpose = launch.ConfigPickerLauncherOverride
	m.configPickerReturnMode = ModePicker

	normal := m
	normal.configPickerCursor = -1
	normalFirst := renderedLineContaining(t, renderConfigPicker(normal), "claude-production")
	normalSecond := renderedLineContaining(t, renderConfigPicker(normal), "codex-sandbox")

	firstPaint := renderConfigPicker(m)
	if got := renderedLineContaining(t, firstPaint, "claude-production"); got != renderSelectedRow(normalFirst) {
		t.Fatalf("initial selected profile row is not normalized:\n got %q\nwant %q", got, renderSelectedRow(normalFirst))
	}

	updated, cmd := m.Update(keyPress("down"))
	m = updated.(Model)
	if m.configPickerCursor != 1 {
		t.Fatalf("cursor after down = %d, want 1", m.configPickerCursor)
	}
	if cmd == nil || reflect.TypeOf(cmd()) != reflect.TypeOf(tea.ClearScreen()) {
		t.Fatal("profile cursor movement must force a full repaint")
	}
	secondPaint := renderConfigPicker(m)
	if got := renderedLineContaining(t, secondPaint, "claude-production"); got != normalFirst {
		t.Fatalf("departed profile row was not restored exactly:\n got %q\nwant %q", got, normalFirst)
	}
	selected := renderedLineContaining(t, secondPaint, "codex-sandbox")
	if selected != renderSelectedRow(normalSecond) {
		t.Fatalf("new profile row is not one highlight across name, harness, path, and marker:\n got %q\nwant %q", selected, renderSelectedRow(normalSecond))
	}
	for _, text := range []string{"codex-sandbox", "🤖 codex", "/profiles/codex", "(current)"} {
		if !strings.Contains(stripAnsi(selected), text) {
			t.Errorf("selected row missing %q: %q", text, stripAnsi(selected))
		}
	}
}

func renderedLineContaining(t *testing.T, rendered, visible string) string {
	t.Helper()
	for _, line := range strings.Split(rendered, "\n") {
		if strings.Contains(stripAnsi(line), visible) {
			return line
		}
	}
	t.Fatalf("rendered output has no line containing %q:\n%s", visible, stripAnsi(rendered))
	return ""
}

// This source-level guard covers each selectable-list family. New semantic
// row styling may be composed freely, but selection must always pass through
// renderSelectedRow instead of directly nesting the reverse style around it.
func TestSelectableListFamiliesUseNormalizedRenderer(t *testing.T) {
	files := []string{
		"chip_picker.go", "model_live_session_picker.go",
		"plan_search.go", "plan_view.go", "worktrees.go",
	}
	for _, name := range files {
		t.Run(name, func(t *testing.T) {
			source, err := os.ReadFile(filepath.Join(".", name))
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			text := string(source)
			if !strings.Contains(text, "renderSelectedRow(") {
				t.Fatalf("%s no longer routes its selected rows through renderSelectedRow", name)
			}
			if strings.Contains(text, "styleSelected.Render(") {
				t.Fatalf("%s directly wraps a styled row with styleSelected", name)
			}
		})
	}
}

package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/sglavoie/agentmux/tui/theme"
)

func TestTerminalBackgroundUpdatesExistingDashboardAndFooterStyles(t *testing.T) {
	// Appearance is shared by every renderer in this process. Run this test
	// before parallel rendering tests so it cannot change their colors between
	// equivalent renders (the atomic palette is race-free but still global).

	theme.Configure("auto")
	t.Cleanup(func() { theme.Configure("auto") })
	m := testModel()
	dark := styleDim.Render("metadata")
	updated, _ := m.Update(tea.BackgroundColorMsg{Color: lipgloss.Color("#FFFFFF")})
	m = updated.(Model)
	if styleDim.Render("metadata") == dark {
		t.Fatal("dashboard style kept dark foreground")
	}
	if !strings.Contains(renderFooterStripeRow("", 20), "48;2;231;236;242") {
		t.Fatal("footer did not adapt its background")
	}
	theme.Configure("dark")
	m.Update(tea.BackgroundColorMsg{Color: lipgloss.Color("#FFFFFF")})
	if styleDim.Render("metadata") != dark {
		t.Fatal("background report ignored explicit override")
	}
}

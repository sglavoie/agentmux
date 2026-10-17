// Package theme holds the adaptive terminal palette every Agent Mux TUI view
// shares, and the base styles built from it.
//
// It colors Agent Mux's own chrome only, never the terminal canvas or captured
// agent output. Like tui/layout it has no in-repo dependency.
package theme

import (
	"image/color"
	"os"
	"strings"
	"sync/atomic"

	"charm.land/lipgloss/v2"
)

const (
	Primary   = "#4EC9B0"
	Secondary = "#7FAEDB"
	Text      = "#F2F4F7"
	Muted     = "#A5B1BE"
	Disabled  = "#8694A2"
	Selection = "#1D3F3C"
	Surface   = "#1B222B"
	Divider   = "#52677C"
	Footer    = "#252D36"
	Amber     = "#FFAA00"
	Red       = "#FF5555"
)

// Selected applies a single, opaque highlight to a normalized row. Callers
// strip embedded ANSI before rendering so inner resets cannot cut the span.
func Selected() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(Color(Text)).Background(Color(Selection)).Bold(true)
}

// Blurred keeps the cursor visible when another pane owns keyboard focus.
func Blurred() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(Color(Muted)).Background(Color(Surface)).Bold(true)
}

func Popup() lipgloss.Style {
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).
		BorderForeground(Color(Secondary)).
		Background(Color(Surface)).Padding(0, 1)
}

// The styles are constructed once across the TUI packages. Adaptive colors
// resolve at render time, so a terminal background response updates all of them.
// Each TUI is a separate process; the atomic value also permits concurrent renders.
var appearance atomic.Int32 // 0 auto/dark, 1 auto/light, 2 forced dark, 3 forced light

var lightPalette = map[string]string{
	Primary: "#006B59", Secondary: "#285C8A", Text: "#202630", Muted: "#4B5663",
	Disabled: "#58636F", Selection: "#D3EAE4", Surface: "#F5F7FA",
	Divider: "#657687", Footer: "#E7ECF2", Amber: "#875100", Red: "#B42332",
}

// Configure accepts auto, light or dark. Unknown values safely use auto.
func Configure(value string) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "dark":
		appearance.Store(2)
	case "light":
		appearance.Store(3)
	default:
		appearance.Store(0)
	}
}
func ConfigureFromEnvironment() { Configure(os.Getenv("AMX_TUI_THEME")) }

// ObserveBackground applies terminal background reports only in automatic mode.
func ObserveBackground(dark bool) {
	next := int32(1)
	if dark {
		next = 0
	}
	for {
		old := appearance.Load()
		if old >= 2 || appearance.CompareAndSwap(old, next) {
			return
		}
	}
}

// Color returns a semantic color that follows the active appearance. Unmapped
// colors (for example configured profile colors) keep their literal value.
func Color(value string) color.Color { return adaptiveColor(value) }

type adaptiveColor string

func (c adaptiveColor) RGBA() (uint32, uint32, uint32, uint32) {
	value := string(c)
	if appearance.Load()%2 == 1 {
		if light, ok := lightPalette[value]; ok {
			value = light
		}
	}
	return lipgloss.Color(value).RGBA()
}

package theme

import (
	"math"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestSelectionAndPopupAreDarkAndKeepGeometry(t *testing.T) {
	selected := Selected().Render("  item  ")
	if strings.Contains(selected, "\x1b[7m") || strings.Contains(selected, "\x1b[27m") {
		t.Fatalf("selection uses reverse video: %q", selected)
	}
	if !strings.Contains(selected, "38;2;242;244;247") || !strings.Contains(selected, "48;2;29;63;60") {
		t.Fatalf("selected row does not use text and tint tokens: %q", selected)
	}
	if ansi.StringWidth(selected) != 8 {
		t.Fatalf("selected row changed width: %q", selected)
	}
	if Blurred().Render("item") == Selected().Render("item") {
		t.Fatal("blurred and focused selection look the same")
	}

	popup := Popup().Width(16).Render("title\nvalue")
	if !strings.Contains(popup, "38;2;127;174;219") || !strings.Contains(popup, "48;2;27;34;43") {
		t.Fatalf("popup lacks blue border or dark surface: %q", popup)
	}
	for _, line := range strings.Split(popup, "\n") {
		if ansi.StringWidth(line) != 16 {
			t.Fatalf("popup line changed width: %q", line)
		}
	}
}

func TestDarkSurfaceContrast(t *testing.T) {
	// Relative luminance, using sRGB gamma, ensures muted metadata and selected
	// text remain legible on the exact fixed dark surfaces used by this theme.
	for _, sample := range []struct {
		foreground, background string
		minimum                float64
	}{{Text, Selection, 4.5}, {Text, Surface, 4.5}, {Muted, Surface, 4.5}, {Disabled, Footer, 4.45}} {
		contrast := (luminance(sample.foreground) + 0.05) / (luminance(sample.background) + 0.05)
		if contrast < sample.minimum {
			t.Errorf("contrast %s on %s = %.2f, want >= %.2f", sample.foreground, sample.background, contrast, sample.minimum)
		}
	}
}

func luminance(hex string) float64 {
	var channels [3]float64
	for i := range channels {
		var n int
		for _, ch := range hex[1+i*2 : 3+i*2] {
			n *= 16
			if ch >= '0' && ch <= '9' {
				n += int(ch - '0')
			} else if ch >= 'A' && ch <= 'F' {
				n += int(ch-'A') + 10
			}
		}
		v := float64(n) / 255
		if v <= 0.04045 {
			channels[i] = v / 12.92
		} else {
			channels[i] = math.Pow((v+0.055)/1.055, 2.4)
		}
	}
	return 0.2126*channels[0] + 0.7152*channels[1] + 0.0722*channels[2]
}

func TestLightAndDarkThemesResolveExistingStylesAndKeepContrast(t *testing.T) {
	t.Cleanup(func() { Configure("auto") })
	style := Selected()
	Configure("dark")
	dark := style.Render("item")
	ObserveBackground(false)
	if style.Render("item") != dark {
		t.Fatal("terminal overrode explicit dark preference")
	}
	Configure("auto")
	ObserveBackground(false)
	light := style.Render("item")
	if light == dark || !strings.Contains(light, "38;2;32;38;48") {
		t.Fatalf("existing style did not adapt: %q", light)
	}
	Configure("light")
	ObserveBackground(true)
	if style.Render("item") != light {
		t.Fatal("terminal overrode explicit light preference")
	}
	for _, bg := range []string{"#FFFFFF", lightPalette[Surface], lightPalette[Selection], lightPalette[Footer]} {
		for _, fg := range []string{Text, Muted, Disabled, Primary, Secondary, Amber, Red} {
			a, b := luminance(lightPalette[fg]), luminance(bg)
			if a < b {
				a, b = b, a
			}
			if ratio := (a + 0.05) / (b + 0.05); ratio < 4.5 {
				t.Errorf("light contrast %s on %s = %.2f", fg, bg, ratio)
			}
		}
	}
	if ansi.StringWidth(light) != ansi.StringWidth(dark) {
		t.Fatal("appearance changed layout")
	}
}

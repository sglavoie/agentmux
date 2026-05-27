package palette

import (
	"encoding/json"
	"fmt"
)

// Color holds the display values for a single named color.
type Color struct {
	ActiveBg   string `json:"active_bg"`
	InactiveBg string `json:"inactive_bg"`
	TextFg     [3]int `json:"text_fg"`
	Emoji      string `json:"emoji"`
}

// ForegroundHex returns the text foreground as a hex color string (#rrggbb).
func (c Color) ForegroundHex() string {
	return fmt.Sprintf("#%02x%02x%02x", c.TextFg[0], c.TextFg[1], c.TextFg[2])
}

type rawPalette struct {
	ActiveFg   string           `json:"active_fg"`
	InactiveFg string           `json:"inactive_fg"`
	Colors     map[string]Color `json:"colors"`
}

var (
	pal rawPalette
	// ActiveFg is the global active-tab foreground hex color.
	ActiveFg string
	// InactiveFg is the global inactive-tab foreground hex color.
	InactiveFg string
)

func init() {
	if err := json.Unmarshal(paletteJSON, &pal); err != nil {
		panic("palette: failed to parse palette.json: " + err.Error())
	}
	ActiveFg = pal.ActiveFg
	InactiveFg = pal.InactiveFg
}

// Lookup returns the Color for the given name, or an error if unknown.
func Lookup(name string) (Color, error) {
	c, ok := pal.Colors[name]
	if !ok {
		return Color{}, fmt.Errorf("unknown color %q", name)
	}
	return c, nil
}

// Names returns all color names in undefined order.
func Names() []string {
	names := make([]string, 0, len(pal.Colors))
	for k := range pal.Colors {
		names = append(names, k)
	}
	return names
}

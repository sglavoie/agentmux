package palette

import (
	"encoding/json"
	"strings"
)

type rawIcons struct {
	Star       string            `json:"star"`
	States     map[string]string `json:"states"`
	StateOrder []string          `json:"state_order"`
}

var ico rawIcons

func init() {
	if err := json.Unmarshal(iconsJSON, &ico); err != nil {
		panic("palette: failed to parse icons.json: " + err.Error())
	}
}

// StarIcon returns the star/pin icon character.
func StarIcon() string { return ico.Star }

// StateIcon returns the icon character for a named state ("bolt", "stop", "key",
// "error", "subagent", "compact"), or "" if the state name is unknown.
func StateIcon(state string) string { return ico.States[state] }

// StateOrder returns the canonical state-detection priority order.
func StateOrder() []string {
	out := make([]string, len(ico.StateOrder))
	copy(out, ico.StateOrder)
	return out
}

// ParseTitle parses a tab title into its components.
// Tab title format: [★][<state-icon>]<base>
// Returns base title, state name ("bolt"/"stop"/"key"/"error"/"subagent"/"compact"/""), and starred flag.
func ParseTitle(title string) (base, state string, starred bool) {
	if strings.HasPrefix(title, ico.Star) {
		starred = true
		title = title[len(ico.Star):]
	}
	for _, name := range ico.StateOrder {
		icon := ico.States[name]
		// Skip empty icons so a stale entry in state_order without a matching
		// glyph cannot prefix-match every title and report a phantom state.
		if icon == "" {
			continue
		}
		if strings.HasPrefix(title, icon) {
			state = name
			title = title[len(icon):]
			break
		}
	}
	base = title
	return
}

// BuildTitle constructs a tab title from its components.
// Output format: [★][<state-icon>]<base>
func BuildTitle(base, state string, starred bool) string {
	var sb strings.Builder
	if starred {
		sb.WriteString(ico.Star)
	}
	if icon, ok := ico.States[state]; ok && state != "" {
		sb.WriteString(icon)
	}
	sb.WriteString(base)
	return sb.String()
}

// StripIcons removes the star prefix and any state icon from a title,
// returning the plain base title.
func StripIcons(title string) string {
	title = strings.TrimPrefix(title, ico.Star)
	for _, name := range ico.StateOrder {
		icon := ico.States[name]
		if icon == "" {
			continue
		}
		if strings.HasPrefix(title, icon) {
			return title[len(icon):]
		}
	}
	return title
}

// DetectState returns the state name present in a title, or "" if none.
func DetectState(title string) string {
	title = strings.TrimPrefix(title, ico.Star)
	for _, name := range ico.StateOrder {
		icon := ico.States[name]
		if icon == "" {
			continue
		}
		if strings.HasPrefix(title, icon) {
			return name
		}
	}
	return ""
}

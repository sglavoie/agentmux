package palette

import (
	"encoding/json"
	"strings"
)

type rawIcons struct {
	Star       string            `json:"star"`
	Lock       string            `json:"lock"`
	Mute       string            `json:"mute"`
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

// LockIcon returns the lock icon character.
func LockIcon() string { return ico.Lock }

// MuteIcon returns the mute icon character.
func MuteIcon() string { return ico.Mute }

// StateIcon returns the icon character for a named state ("bolt", "stop", "key",
// "warning", "error", "subagent", "compact"), or "" if the state name is
// unknown.
func StateIcon(state string) string { return ico.States[state] }

// StateOrder returns the canonical state-detection priority order.
func StateOrder() []string {
	out := make([]string, len(ico.StateOrder))
	copy(out, ico.StateOrder)
	return out
}

// ParseTitle parses a tab title into its components.
// Tab title format: [★][🔇][<state-icon>]<base>
// Returns base title, state name
// ("bolt"/"stop"/"key"/"warning"/"error"/"subagent"/"compact"/"question"/""),
// and starred flag. Use ParseTitleFull to also recover the muted flag.
func ParseTitle(title string) (base, state string, starred bool) {
	base, state, starred, _ = ParseTitleFull(title)
	return
}

// ParseTitleFull parses a tab title into its components, including the muted
// flag carried by an optional 🔇 prefix.
// Tab title format: [★][🔇][<state-icon>]<base>
func ParseTitleFull(title string) (base, state string, starred, muted bool) {
	if ico.Star != "" && strings.HasPrefix(title, ico.Star) {
		starred = true
		title = title[len(ico.Star):]
	}
	if ico.Mute != "" && strings.HasPrefix(title, ico.Mute) {
		muted = true
		title = title[len(ico.Mute):]
	}
	if name, rest, ok := consumeStateIcon(title); ok {
		state = name
		title = rest
	}
	base = title
	return
}

// BuildTitle constructs a tab title from its components.
// Output format: [★][<state-icon>]<base>. Use BuildTitleFull to include the
// 🔇 mute prefix.
func BuildTitle(base, state string, starred bool) string {
	return BuildTitleFull(base, state, starred, false)
}

// BuildTitleFull constructs a tab title from its components, including an
// optional 🔇 mute prefix. Output format: [★][🔇][<state-icon>]<base>.
func BuildTitleFull(base, state string, starred, muted bool) string {
	var sb strings.Builder
	if starred {
		sb.WriteString(ico.Star)
	}
	if muted && ico.Mute != "" {
		sb.WriteString(ico.Mute)
	}
	if icon, ok := ico.States[state]; ok && state != "" {
		sb.WriteString(icon)
	}
	sb.WriteString(base)
	return sb.String()
}

// StripIcons removes the star prefix, optional mute prefix, and any state
// icon from a title, returning the plain base title.
func StripIcons(title string) string {
	if ico.Star != "" {
		title = strings.TrimPrefix(title, ico.Star)
	}
	if ico.Mute != "" {
		title = strings.TrimPrefix(title, ico.Mute)
	}
	if _, rest, ok := consumeStateIcon(title); ok {
		return rest
	}
	return title
}

// DetectState returns the state name present in a title, or "" if none.
func DetectState(title string) string {
	if ico.Star != "" {
		title = strings.TrimPrefix(title, ico.Star)
	}
	if ico.Mute != "" {
		title = strings.TrimPrefix(title, ico.Mute)
	}
	name, _, ok := consumeStateIcon(title)
	if ok {
		return name
	}
	return ""
}

func consumeStateIcon(title string) (state, rest string, ok bool) {
	for _, name := range ico.StateOrder {
		icon := ico.States[name]
		// Skip empty icons so a stale entry in state_order without a matching
		// glyph cannot prefix-match every title and report a phantom state.
		if icon == "" {
			continue
		}
		if strings.HasPrefix(title, icon) {
			return name, title[len(icon):], true
		}
		fallback := strings.ReplaceAll(icon, "\ufe0f", "")
		if fallback != icon && fallback != "" && strings.HasPrefix(title, fallback) {
			rest := title[len(fallback):]
			rest = strings.TrimPrefix(rest, "\ufe0f")
			rest = strings.TrimPrefix(rest, "\ufe0e")
			return name, rest, true
		}
	}
	return "", title, false
}

package palette

import (
	"encoding/json"
	"strings"
)

type rawIcons struct {
	Star       string            `json:"star"`
	Lock       string            `json:"lock"`
	Mute       string            `json:"mute"`
	Loop       string            `json:"loop"`
	States     map[string]string `json:"states"`
	StateOrder []string          `json:"state_order"`
}

var ico rawIcons

type TitleParts struct {
	Base    string
	State   string
	Starred bool
	Locked  bool
	Muted   bool
	Looping bool
}

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

// LoopIcon returns the active loop marker character.
func LoopIcon() string { return ico.Loop }

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
// Tab title format: [★][🔒][🔇][<state-icon>][🔁]<base>
// Returns base title, state name
// ("bolt"/"stop"/"key"/"warning"/"error"/"subagent"/"compact"/"question"/""),
// and starred flag. Use ParseTitleParts to recover every marker.
func ParseTitle(title string) (base, state string, starred bool) {
	base, state, starred, _ = ParseTitleFull(title)
	return
}

// ParseTitleParts parses a tab title into its complete icon/title components.
// Leading icon markers are accepted in canonical order and in legacy orders;
// each marker kind is consumed at most once.
func ParseTitleParts(title string) TitleParts {
	parts := TitleParts{Base: title}
	for {
		switch {
		case !parts.Starred && ico.Star != "" && strings.HasPrefix(title, ico.Star):
			parts.Starred = true
			title = title[len(ico.Star):]
		case !parts.Locked && ico.Lock != "" && strings.HasPrefix(title, ico.Lock):
			parts.Locked = true
			title = title[len(ico.Lock):]
		case !parts.Muted && ico.Mute != "" && strings.HasPrefix(title, ico.Mute):
			parts.Muted = true
			title = title[len(ico.Mute):]
		case parts.State == "":
			if state, rest, ok := consumeStateIcon(title); ok {
				parts.State = state
				title = rest
				continue
			}
			if !parts.Looping && ico.Loop != "" && strings.HasPrefix(title, ico.Loop) {
				parts.Looping = true
				title = title[len(ico.Loop):]
				continue
			}
			parts.Base = title
			return parts
		case !parts.Looping && ico.Loop != "" && strings.HasPrefix(title, ico.Loop):
			parts.Looping = true
			title = title[len(ico.Loop):]
		default:
			parts.Base = title
			return parts
		}
	}
}

// ParseTitleFull parses a tab title into its components, including the muted
// flag carried by an optional 🔇 prefix.
// Tab title format: [★][🔒][🔇][<state-icon>][🔁]<base>.
// Use ParseTitleParts to also recover the lock and loop flags.
func ParseTitleFull(title string) (base, state string, starred, muted bool) {
	parts := ParseTitleParts(title)
	return parts.Base, parts.State, parts.Starred, parts.Muted
}

// BuildTitle constructs a tab title from its components.
// Output format: [★][<state-icon>]<base>. Use BuildTitleFull to include the
// 🔇 mute prefix.
func BuildTitle(base, state string, starred bool) string {
	return BuildTitleFull(base, state, starred, false)
}

// BuildTitleParts constructs a tab title from its complete components.
// Output format: [★][🔒][🔇][<state-icon>][🔁]<base>.
func BuildTitleParts(parts TitleParts) string {
	var sb strings.Builder
	if parts.Starred && ico.Star != "" {
		sb.WriteString(ico.Star)
	}
	if parts.Locked && ico.Lock != "" {
		sb.WriteString(ico.Lock)
	}
	if parts.Muted && ico.Mute != "" {
		sb.WriteString(ico.Mute)
	}
	if icon, ok := ico.States[parts.State]; ok && parts.State != "" {
		sb.WriteString(icon)
	}
	if parts.Looping && ico.Loop != "" {
		sb.WriteString(ico.Loop)
	}
	sb.WriteString(parts.Base)
	return sb.String()
}

// BuildTitleFull constructs a tab title from its components, including an
// optional 🔇 mute prefix. Output format: [★][🔇][<state-icon>]<base>.
func BuildTitleFull(base, state string, starred, muted bool) string {
	return BuildTitleParts(TitleParts{
		Base:    base,
		State:   state,
		Starred: starred,
		Muted:   muted,
	})
}

// StripIcons removes leading palette icons from a title, returning the plain
// base title.
func StripIcons(title string) string {
	return ParseTitleParts(title).Base
}

// DetectState returns the state name present in a title, or "" if none.
func DetectState(title string) string {
	return ParseTitleParts(title).State
}

func consumeStateIcon(title string) (state, rest string, ok bool) {
	bestLen := -1
	for _, name := range ico.StateOrder {
		icon := ico.States[name]
		// Skip empty icons so a stale entry in state_order without a matching
		// glyph cannot prefix-match every title and report a phantom state.
		if icon == "" {
			continue
		}
		if strings.HasPrefix(title, icon) {
			if len(icon) > bestLen {
				state = name
				rest = title[len(icon):]
				ok = true
				bestLen = len(icon)
			}
			continue
		}
		fallback := strings.ReplaceAll(icon, "\ufe0f", "")
		if fallback != icon && fallback != "" && strings.HasPrefix(title, fallback) {
			candidateRest := title[len(fallback):]
			candidateRest = strings.TrimPrefix(candidateRest, "\ufe0f")
			candidateRest = strings.TrimPrefix(candidateRest, "\ufe0e")
			matchedLen := len(title) - len(candidateRest)
			if matchedLen > bestLen {
				state = name
				rest = candidateRest
				ok = true
				bestLen = matchedLen
			}
		}
	}
	if ok {
		return state, rest, true
	}
	return "", title, false
}

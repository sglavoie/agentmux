package session

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/sglavoie/agentmux/internal/kitty"
)

// SortKey computes the sort key for a tab with the given account, base title,
// and starred flag. mode is "alpha" or "account".
//
// Pinned (starred) tabs sort before non-pinned via a "0:" vs "1:" prefix.
// Alpha mode sorts by full base title; account mode groups by account name first.
func SortKey(account, baseTitle string, starred bool, mode string) string {
	pin := "1"
	if starred {
		pin = "0"
	}
	proj := projectName(baseTitle)
	projKey := projectSortKey(proj)
	switch mode {
	case "account":
		return pin + ":" + strings.ToLower(account) + ":" + projKey
	default: // "alpha"
		// Preserve the rendered "<initial>:" profile prefix from baseTitle
		// so alpha mode still groups by tab-prefix when present, but
		// normalize the project portion (drop leading emoji/symbols).
		var prefix string
		if len(baseTitle) > len(proj) {
			prefix = strings.ToLower(baseTitle[:len(baseTitle)-len(proj)])
		}
		return pin + ":" + prefix + projKey
	}
}

// ComputeInsertLocation determines where a new tab should be inserted within
// the target Kitty OS window to maintain sorted order. New tabs are never
// pinned (starred=false).
//
// Returns ("before", tabID) when the new tab sorts before all existing managed
// tabs in that window, ("after", tabID) when it should follow tabID, or
// ("", 0) when the target window has no managed tabs yet.
func ComputeInsertLocation(client kitty.KittyClient, mode, account, baseTitle string) (string, int, error) {
	osWindows, err := client.ListWindows()
	if err != nil {
		return "", 0, err
	}
	target := targetOSWindow(osWindows)
	if target == nil {
		return "", 0, nil
	}
	running, _ := runningSessionsFromTabs(target.Tabs, nil)

	newKey := SortKey(account, baseTitle, false, mode)

	type entry struct {
		key   string
		tabID int
	}
	var entries []entry
	for _, s := range running {
		k := SortKey(s.Account, s.Base, s.Starred, mode)
		entries = append(entries, entry{key: k, tabID: s.TabID})
	}

	// Sort existing entries by key to find insertion point.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].key < entries[j].key
	})

	prevID := 0
	for _, e := range entries {
		if e.key > newKey {
			break
		}
		prevID = e.tabID
	}

	if prevID == 0 {
		if len(entries) == 0 {
			return "", 0, nil
		}
		// New tab sorts before all existing session tabs; insert before the first one.
		return "before", entries[0].tabID, nil
	}
	return "after", prevID, nil
}

// SortRunning returns a copy of sessions sorted by the given mode.
func SortRunning(sessions []RunningSession, mode string) []RunningSession {
	sorted := make([]RunningSession, len(sessions))
	copy(sorted, sessions)
	sort.SliceStable(sorted, func(i, j int) bool {
		ki := SortKey(sorted[i].Account, sorted[i].Base, sorted[i].Starred, mode)
		kj := SortKey(sorted[j].Account, sorted[j].Base, sorted[j].Starred, mode)
		return ki < kj
	})
	return sorted
}

// projectName strips the leading "<rune>:" profile prefix from a base title.
// The prefix is a single letter rune (uppercase or caseless) followed by a
// colon, matching the output of ProfilePrefix. Unicode profile initials such
// as "É:project" or "中:project" are handled in addition to plain ASCII
// like "W:api". Returns base unchanged when no such prefix is present.
func projectName(base string) string {
	r, size := utf8.DecodeRuneInString(base)
	if r == utf8.RuneError {
		return base
	}
	if len(base) < size+1 || base[size] != ':' {
		return base
	}
	// The rune must be a letter; reject numeric or punctuation prefixes
	// (e.g. "1:foo" or "?:foo") that ProfilePrefix would never produce.
	if !unicode.IsLetter(r) {
		return base
	}
	// For cased scripts (Latin, Cyrillic, Greek), the rune must already be
	// uppercase — lowercase prefixes like "w:api" do not come from
	// ProfilePrefix and should not be treated as a profile prefix.
	if unicode.IsLower(r) {
		return base
	}
	return base[size+1:]
}

// projectSortKey returns the lowercased project name with leading
// non-letter, non-digit runes (emoji, punctuation, symbols, combining
// marks) stripped. Intra-word characters are preserved. When stripping
// would leave an empty string, the full lowercased input is returned
// so a title made entirely of symbols still has a stable key.
func projectSortKey(project string) string {
	i := 0
	for i < len(project) {
		r, size := utf8.DecodeRuneInString(project[i:])
		if r == utf8.RuneError {
			break
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			break
		}
		i += size
	}
	if i >= len(project) {
		return strings.ToLower(project)
	}
	return strings.ToLower(project[i:])
}

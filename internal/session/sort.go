package session

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

// SortKey computes the sort key for a tab with the given account, base title,
// and starred flag. mode is "alpha" or "account".
//
// Pinned (starred) tabs sort before non-pinned via a "0:" vs "1:" prefix.
// Alpha mode sorts by project title first; account mode groups by account name
// first. Legacy "<initial>:<project>" titles are normalized to their project
// portion when the prefix matches the supplied account/profile.
func SortKey(account, baseTitle string, starred bool, mode string) string {
	pin := "1"
	if starred {
		pin = "0"
	}
	proj := projectNameForProfile(baseTitle, account)
	projKey := ProjectSortKey(proj)
	accountKey := strings.ToLower(account)
	projectKey := strings.ToLower(proj)
	switch mode {
	case "account":
		return pin + ":" + accountKey + ":" + projKey + ":" + projectKey
	default: // "alpha"
		return pin + ":" + projKey + ":" + accountKey + ":" + projectKey
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
	running, _ := runningSessionsFromTabs(target.Tabs, projectMatchContext{})

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

	// Sort existing entries by key to find insertion point. Stable so ties
	// preserve original order (matters for ComputeInsertLocation determinism
	// when multiple tabs share a sort key — without stability, the chosen
	// prevID drifts between calls).
	sort.SliceStable(entries, func(i, j int) bool {
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

// OrderEntry is what the session order reads of one session: the profile it
// runs under, its base title, and whether it is pinned.
type OrderEntry struct {
	Profile string
	Base    string
	Starred bool
	// Tie places a session among those the rest cannot tell apart: the
	// runtime-session ID of a session on a backend with no tabs, empty for a
	// Kitty tab, whose place among its equals is its tab's.
	Tie string
}

// Order sorts items in place into the one order sessions are listed in:
// pinned first, then by project title ("alpha") or by profile ("account"),
// as SortKey spells it. Among sessions with equal keys, those with no Tie
// come first and keep their relative order, and the rest follow by Tie:
// nothing finds a backend's sessions in an order two readers share, so the
// order they arrive in decides nothing.
//
// The dashboard, session navigation, and the daemon's companion projection
// all order through here, so the session after this one is the same session
// on every surface. The order is a fact AgentMux computes, not something a
// terminal is asked to hold: Kitty's tabs are moved into it (Resort), and a
// backend with no tabs has nothing to move.
func Order[T any](items []T, mode string, entry func(T) OrderEntry) {
	keys := make(map[OrderEntry]string, len(items))
	key := func(e OrderEntry) string {
		e.Tie = ""
		k, ok := keys[e]
		if !ok {
			k = SortKey(e.Profile, e.Base, e.Starred, mode)
			keys[e] = k
		}
		return k
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := entry(items[i]), entry(items[j])
		if ka, kb := key(a), key(b); ka != kb {
			return ka < kb
		}
		return a.Tie < b.Tie
	})
}

// SortRunning returns a copy of sessions in the session order (Order).
func SortRunning(sessions []RunningSession, mode string) []RunningSession {
	sorted := make([]RunningSession, len(sessions))
	copy(sorted, sessions)
	Order(sorted, mode, func(s RunningSession) OrderEntry {
		e := OrderEntry{Profile: s.Account, Base: s.Base, Starred: s.Starred}
		if s.Backend != "" && s.Backend != runtimesession.BackendKitty {
			e.Tie = string(s.RuntimeSessionID)
		}
		return e
	})
	return sorted
}

// projectName returns the project portion of a modern base title. Managed Kitty
// tabs now use project-only base titles, so the raw base string is already the
// project name. Legacy prefix stripping lives in projectNameForProfile.
func projectName(base string) string {
	return base
}

// legacyProjectName strips the old "<ProfileInitial>:" prefix when it matches
// the supplied profile/account. The boolean reports whether the prefix was
// recognized and removed.
func legacyProjectName(base, profile string) (string, bool) {
	prefix := ProfilePrefix(profile)
	if prefix == "" || !strings.HasPrefix(base, prefix) {
		return base, false
	}
	return base[len(prefix):], true
}

func projectNameForProfile(base, profile string) string {
	if project, ok := legacyProjectName(base, profile); ok {
		return project
	}
	return projectName(base)
}

// ProjectSortKey returns the lowercased project name with leading
// non-letter, non-digit runes (emoji, punctuation, symbols, combining
// marks) stripped. Intra-word characters are preserved. When stripping
// would leave an empty string, the full lowercased input is returned
// so a title made entirely of symbols still has a stable key.
func ProjectSortKey(project string) string {
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

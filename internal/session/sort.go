package session

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// SortKey computes the sort key for a session with the given profile, title,
// and starred flag. mode is "alpha" or "account".
//
// Pinned sessions sort before unpinned via a "0:" vs "1:" prefix.
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

// OrderEntry is what the session order reads of one session: the profile it
// runs under, its base title, and whether it is pinned.
type OrderEntry struct {
	Profile string
	Base    string
	Starred bool
	// Tie places a session among those the rest cannot tell apart: the
	// runtime-session ID, used to break otherwise equal sort keys.
	Tie string
}

// Order sorts items in place into the one order sessions are listed in:
// pinned first, then by project title ("alpha") or by profile ("account"),
// as SortKey spells it. Sessions with equal keys follow their Tie;
// arrival order does not affect the result.
//
// The dashboard, session navigation, and the daemon's companion projection
// all order through here, so the session after this one is the same session
// on every surface.
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
		return OrderEntry{Profile: s.Account, Base: s.Base, Starred: s.Starred, Tie: string(s.RuntimeSessionID)}
	})
	return sorted
}

// projectName returns the project portion of a modern base title.
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

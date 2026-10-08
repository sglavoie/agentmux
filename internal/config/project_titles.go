package config

import (
	"sort"
	"strings"
	"unicode"
)

// ProjectTitleKey is how an operator types a project title: without the
// emoji, bullets, and other symbols titles often lead with, and in lower case.
// "🐱amx", "•amx", and "AMX" all read as "amx". Commands that take a typed
// title compare by it only after an exact title has failed to match, so it
// can never shadow a project actually titled that way.
func ProjectTitleKey(title string) string {
	trimmed := strings.TrimLeftFunc(strings.TrimSpace(title), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	return strings.ToLower(trimmed)
}

// CompleteProjectTitle is what shell completion offers for title when the
// operator has typed typed, and whether it offers anything. A title that
// starts with typed is offered as it is. One that does once its leading emoji
// or symbols are set aside, ignoring case, is offered as typed followed by the
// rest of it: "ras" completes "🥧Raspberry Pi" to "raspberry Pi", which keeps
// the prefix the shell filters on and selects the same project, since titles
// typed that way are read by their ProjectTitleKey.
func CompleteProjectTitle(title, typed string) (string, bool) {
	if strings.HasPrefix(title, typed) {
		return title, true
	}
	rest := []rune(strings.TrimLeftFunc(strings.TrimSpace(title), func(r rune) bool { return !isTitleWordRune(r) }))
	prefix := []rune(typed)
	if len(prefix) > len(rest) || !strings.EqualFold(string(rest[:len(prefix)]), typed) {
		return "", false
	}
	return typed + string(rest[len(prefix):]), true
}

// ProjectTitleTypo reports how many edits separate typed from title once both
// are read by their ProjectTitleKey, and whether that is few enough to call
// typed a typo of title: two, or a third of what was typed when that is more.
// The title's leading words count on their own, since a typed title is most
// often its first word: "rasberry" is one edit from "🥧Raspberry Pi", though
// four from the whole of it. An empty key is no typo of anything.
func ProjectTitleTypo(typed, title string) (int, bool) {
	key := ProjectTitleKey(typed)
	if key == "" {
		return 0, false
	}
	limit := max(2, len([]rune(key))/3)
	best := -1
	for _, lead := range titleLeads(ProjectTitleKey(title)) {
		if d := editDistance(key, lead); best < 0 || d < best {
			best = d
		}
	}
	return best, best >= 0 && best <= limit
}

// ProjectTitleNearMisses returns the titles typed most likely meant when it
// matched none of them: every title containing it, once both are read by their
// ProjectTitleKey, or failing any, every title it is a ProjectTitleTypo of,
// closest first. Ties keep the order titles came in. They are suggestions only;
// no caller resolves a selector to one.
func ProjectTitleNearMisses(titles []string, typed string) []string {
	key := ProjectTitleKey(typed)
	if key == "" {
		return nil
	}
	var near []string
	for _, title := range titles {
		if strings.Contains(ProjectTitleKey(title), key) {
			near = append(near, title)
		}
	}
	if len(near) > 0 {
		return near
	}
	distance := map[string]int{}
	for _, title := range titles {
		if d, ok := ProjectTitleTypo(typed, title); ok {
			near = append(near, title)
			distance[title] = d
		}
	}
	sort.SliceStable(near, func(i, j int) bool { return distance[near[i]] < distance[near[j]] })
	return near
}

// titleLeads is key and every leading run of its words: "raspberry pi" gives
// "raspberry" and "raspberry pi", "dev-helpers" gives "dev" and "dev-helpers".
func titleLeads(key string) []string {
	if key == "" {
		return nil
	}
	var leads []string
	runes := []rune(key)
	for i := 1; i < len(runes); i++ {
		if isTitleWordRune(runes[i-1]) && !isTitleWordRune(runes[i]) {
			leads = append(leads, string(runes[:i]))
		}
	}
	return append(leads, key)
}

func isTitleWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// editDistance is the Levenshtein distance between a and b, counted in runes.
func editDistance(a, b string) int {
	ar, br := []rune(a), []rune(b)
	row := make([]int, len(br)+1)
	for j := range row {
		row[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		diagonal := row[0]
		row[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			diagonal, row[j] = row[j], min(row[j]+1, row[j-1]+1, diagonal+cost)
		}
	}
	return row[len(br)]
}

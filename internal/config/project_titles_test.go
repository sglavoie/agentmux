package config

import (
	"strings"
	"testing"
)

func TestEditDistance(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{{"", "", 0}, {"amx", "amx", 0}, {"amxx", "amx", 1}, {"brainote", "brainnotes", 2}, {"kitten", "sitting", 3}, {"", "abc", 3}, {"é", "e", 1}} {
		if got := editDistance(tc.a, tc.b); got != tc.want {
			t.Errorf("editDistance(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

// TestProjectTitleTypoReadsLeadingWords pins that a typo of a title's first
// words is one, though the rest of the title puts it past the limit.
func TestProjectTitleTypoReadsLeadingWords(t *testing.T) {
	for _, tc := range []struct {
		typed, title string
		want         int
		ok           bool
	}{
		{"rasberry", "🥧Raspberry Pi", 1, true},
		{"devhelpers", "🧰dev-helpers", 1, true},
		{"sglavoi", "🌐sglavoie.com", 1, true},
		{"amxx", "🐱amx", 1, true},
		{"rasberry pi", "🥧Raspberry Pi", 1, true},
		{"muse", "🥧Raspberry Pi", 0, false},
		{"", "🐱amx", 0, false},
		{"🐱", "🐱amx", 0, false},
	} {
		got, ok := ProjectTitleTypo(tc.typed, tc.title)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("ProjectTitleTypo(%q, %q) = %d, %v; want %d, %v", tc.typed, tc.title, got, ok, tc.want, tc.ok)
		}
	}
}

func TestProjectTitleNearMissesPreferContainingTitles(t *testing.T) {
	titles := []string{"🐱amx", "Amp", "🥧Raspberry Pi", "Lyzma"}
	for _, tc := range []struct{ typed, want string }{
		{"rasp", "🥧Raspberry Pi"},
		{"a", "🐱amx,Amp,🥧Raspberry Pi,Lyzma"},
		{"amxx", "🐱amx,Amp"},
		{"rasberry", "🥧Raspberry Pi"},
		{"zzzzzzzz", ""},
		{"  ", ""},
	} {
		if got := strings.Join(ProjectTitleNearMisses(titles, tc.typed), ","); got != tc.want {
			t.Errorf("ProjectTitleNearMisses(%q) = %q, want %q", tc.typed, got, tc.want)
		}
	}
}

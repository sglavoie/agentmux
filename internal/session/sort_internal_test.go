package session

import "testing"

// TestProjectName_ProjectOnlyContract pins the new base-title contract:
// projectName treats the stripped Kitty title as an exact project title and
// does not try to infer or remove a profile prefix on its own.
func TestProjectName_ProjectOnlyContract(t *testing.T) {
	cases := []struct {
		base string
		want string
	}{
		{"api", "api"},
		{"A:b:c", "A:b:c"},
		{"w:api", "w:api"},
		{"W:api", "W:api"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := projectName(tc.base); got != tc.want {
			t.Errorf("projectName(%q) = %q, want %q", tc.base, got, tc.want)
		}
	}
}

// TestLegacyProjectName_ProfileAware keeps the old prefix-stripping logic as an
// explicit compatibility path. Prefixes only strip when they match the known
// profile/account for the tab.
func TestLegacyProjectName_ProfileAware(t *testing.T) {
	cases := []struct {
		base     string
		profile  string
		want     string
		stripped bool
	}{
		{"W:api", "work", "api", true},
		{"É:scriba", "éscriba", "scriba", true},
		{"中:project", "中文", "project", true},
		{"A:b:c", "alpha", "b:c", true},
		{"W:api", "personal", "W:api", false},
		{"w:api", "work", "w:api", false},
		{"1:foo", "work", "1:foo", false},
		{"api", "work", "api", false},
	}
	for _, tc := range cases {
		got, stripped := legacyProjectName(tc.base, tc.profile)
		if got != tc.want || stripped != tc.stripped {
			t.Errorf("legacyProjectName(%q, %q) = (%q, %v), want (%q, %v)",
				tc.base, tc.profile, got, stripped, tc.want, tc.stripped)
		}
	}
}

// TestProjectSortKey verifies the leading-non-alphanumeric stripping
// contract: emoji, punctuation, and symbol runes at the start of a
// project name must not contribute to the sort key, but intra-word
// punctuation must be preserved, and an all-symbol title must collapse
// to its lowercased self (rather than the empty string) so it still
// has a stable, comparable key.
func TestProjectSortKey(t *testing.T) {
	cases := []struct {
		project string
		want    string
	}{
		{"🐱amx", "amx"},
		{"📝tt", "tt"},
		{"🎵muse", "muse"},
		{"🐱", "🐱"},
		{"a-b", "a-b"},
		{"API", "api"},
		{"", ""},
		{"   leading-space", "leading-space"},
		{"---only-symbols---", "only-symbols---"},
		{"🐱📝amx", "amx"},
	}
	for _, tc := range cases {
		if got := ProjectSortKey(tc.project); got != tc.want {
			t.Errorf("ProjectSortKey(%q) = %q, want %q", tc.project, got, tc.want)
		}
	}
}

// TestSortKey_UnicodePrefix verifies that legacy Unicode profile prefixes still
// normalize to the project portion when the caller supplies the matching
// account/profile.
func TestSortKey_UnicodePrefix(t *testing.T) {
	// Same profile "中文", two projects "alpha" and "beta".
	keyAlpha := SortKey("中文", "中:alpha", false, "account")
	keyBeta := SortKey("中文", "中:beta", false, "account")
	if keyAlpha >= keyBeta {
		t.Errorf("expected %q < %q", keyAlpha, keyBeta)
	}
}

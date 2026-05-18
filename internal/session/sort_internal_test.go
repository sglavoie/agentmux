package session

import "testing"

// TestProjectName_Unicode pins the Unicode-prefix contract for projectName:
// the helper must strip profile initials produced by ProfilePrefix, which
// can be any uppercase or caseless letter rune (Latin, CJK, etc.), not only
// ASCII A–Z. Prior versions matched a byte-level [A-Z] which broke sort
// keys and EffectiveProject() for non-ASCII profile names.
func TestProjectName_Unicode(t *testing.T) {
	cases := []struct {
		base string
		want string
	}{
		// ASCII profile prefixes still strip.
		{"W:api", "api"},
		{"P:dotfiles", "dotfiles"},

		// Multibyte uppercase Latin prefix.
		{"É:scriba", "scriba"},
		// CJK profile prefix (caseless rune; treated as profile initial).
		{"中:project", "project"},

		// Lowercase ASCII prefix is NOT produced by ProfilePrefix and must
		// not be treated as a profile prefix.
		{"w:api", "w:api"},

		// Non-letter leading runes must not be stripped.
		{"1:foo", "1:foo"},
		{"?:foo", "?:foo"},

		// No colon → unchanged.
		{"myproject", "myproject"},
		{"", ""},

		// Single-rune base ("W:" with nothing after) collapses to "".
		{"W:", ""},
	}
	for _, tc := range cases {
		if got := projectName(tc.base); got != tc.want {
			t.Errorf("projectName(%q) = %q, want %q", tc.base, got, tc.want)
		}
	}
}

// TestSortKey_UnicodePrefix verifies that account-mode sort keys for
// Unicode-prefixed titles use the project portion (not the prefix) as the
// inner sort component, matching the ASCII behavior. Two sessions on the
// same profile should sort by project name regardless of the prefix's
// underlying byte length.
func TestSortKey_UnicodePrefix(t *testing.T) {
	// Same profile "中文", two projects "alpha" and "beta".
	keyAlpha := SortKey("中文", "中:alpha", false, "account")
	keyBeta := SortKey("中文", "中:beta", false, "account")
	if keyAlpha >= keyBeta {
		t.Errorf("expected %q < %q", keyAlpha, keyBeta)
	}
}

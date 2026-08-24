package screenstate

import (
	"slices"
	"testing"
)

// composerLineNBSP is Claude 2.1.x's empty composer with the non-breaking space it
// actually emits after the ❯ glyph. It is written as an escape so the padding
// that makes this package necessary stays visible in the source.
const composerLineNBSP = "❯\u00a0"

func TestNormalizeLines(t *testing.T) {
	cases := []struct {
		name   string
		screen string
		want   []string
	}{
		{
			name:   "right-trims ascii padding",
			screen: "first   \nsecond\t\t\n",
			want:   []string{"first", "second"},
		},
		{
			name: "right-trims the non-breaking space after the composer glyph",
			// The observed flicker: Claude 2.1.x pads the composer line with
			// U+00A0, which a " \t" cut set (or Go's ASCII-only \s) would keep.
			screen: composerLineNBSP + "\n",
			want:   []string{"❯"},
		},
		{
			name:   "drops trailing blank lines from get-text row padding",
			screen: "body\n\n   \n\t\n\n",
			want:   []string{"body"},
		},
		{
			name:   "preserves leading whitespace and interior blanks",
			screen: "  indented\n\n    deeper\n",
			want:   []string{"  indented", "", "    deeper"},
		},
		{
			name:   "handles CRLF captures",
			screen: "first\r\nsecond\r\n",
			want:   []string{"first", "second"},
		},
		{
			name:   "empty input normalizes to no lines",
			screen: "",
			want:   []string{},
		},
		{
			name:   "all-blank input normalizes to no lines",
			screen: "\n \n\t\n",
			want:   []string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := NormalizeLines(tc.screen)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("NormalizeLines(%q) = %q, want %q", tc.screen, got, tc.want)
			}
		})
	}
}

// TestFingerprintIgnoresOnlyTrailingWhitespace pins the equality semantics the
// stabilization gate depends on: screens that differ only in the padding
// NormalizeLines removes must agree, and screens that differ in any structural
// way must not.
func TestFingerprintIgnoresOnlyTrailingWhitespace(t *testing.T) {
	const base = "✻ Baked for 16m 32s\n\n❯\n\n  ? for shortcuts\n"
	equal := []struct {
		name   string
		screen string
	}{
		{name: "trailing ascii space after the composer", screen: "✻ Baked for 16m 32s\n\n❯ \n\n  ? for shortcuts\n"},
		{name: "non-breaking space after the composer", screen: "✻ Baked for 16m 32s\n\n" + composerLineNBSP + "\n\n  ? for shortcuts\n"},
		{name: "extra get-text row padding", screen: base + "\n\n\n"},
		{name: "CRLF line endings", screen: "✻ Baked for 16m 32s\r\n\r\n❯\r\n\r\n  ? for shortcuts\r\n"},
	}
	for _, tc := range equal {
		t.Run(tc.name, func(t *testing.T) {
			if got, want := Fingerprint(tc.screen), Fingerprint(base); got != want {
				t.Fatalf("Fingerprint(%q) = %d, want %d (same normalized screen)", tc.screen, got, want)
			}
		})
	}

	differ := []struct {
		name   string
		screen string
	}{
		{name: "elapsed timer advanced", screen: "✻ Baked for 16m 33s\n\n❯\n\n  ? for shortcuts\n"},
		{name: "leading whitespace is structural", screen: "✻ Baked for 16m 32s\n\n  ❯\n\n  ? for shortcuts\n"},
		{name: "interior blank line is structural", screen: "✻ Baked for 16m 32s\n❯\n\n  ? for shortcuts\n"},
	}
	for _, tc := range differ {
		t.Run(tc.name, func(t *testing.T) {
			if got := Fingerprint(tc.screen); got == Fingerprint(base) {
				t.Fatalf("Fingerprint(%q) = %d, want a value distinct from the base screen", tc.screen, got)
			}
		})
	}
}

// TestFingerprintNeverReturnsTheDisabledSentinel guards the daemon contract:
// AuthoritativeIdle.Generation reads 0 as "generation check disabled", so a
// screen that hashed to zero would silently switch the gate off.
func TestFingerprintNeverReturnsTheDisabledSentinel(t *testing.T) {
	for _, screen := range []string{"", "\n", "   \n\t\n", composerLineNBSP + "\n", "a"} {
		if got := Fingerprint(screen); got == 0 {
			t.Fatalf("Fingerprint(%q) = 0, want a non-zero generation", screen)
		}
	}
}

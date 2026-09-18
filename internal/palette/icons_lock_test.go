package palette

import "testing"

func TestLockedTitleRoundTrip(t *testing.T) {
	want := TitleParts{Base: "🦊api", State: "bolt", Starred: true, Locked: true, Muted: true, Looping: true}
	for _, title := range []string{"★🔒🔇⚡🔁🦊api", "🔁🔒★🔇⚡🦊api"} {
		if got := ParseTitleParts(title); got != want {
			t.Fatalf("ParseTitleParts(%q) = %+v, want %+v", title, got, want)
		}
		if got := StripIcons(title); got != "🦊api" {
			t.Fatalf("StripIcons(%q) = %q", title, got)
		}
	}
	if got := BuildTitleParts(want); got != "★🔒🔇⚡🔁🦊api" {
		t.Fatalf("BuildTitleParts = %q", got)
	}
}

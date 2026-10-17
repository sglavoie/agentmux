package history

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/layout"
)

func TestFullMetadataCanBeReadAcrossNarrowPagesWithoutEvents(t *testing.T) {
	profile := "production-" + strings.Repeat("long-profile-", 10)
	directory := "/projects/" + strings.Repeat("世界/checkouts/", 12)
	in := DetailInput{ViewMetadata: true, WindowID: 1, Width: 60, Height: 24, BodyHeight: 21,
		Sessions: []session.RunningSession{{WindowID: 1, Base: "api", Profile: profile, Directory: directory, Branch: "feature/polish", Locked: true, Muted: true}}}
	// Every metadata row must be reachable even when the event ring is empty.
	var read strings.Builder
	total, _ := in.ScrollBounds(in.VisibleHeight())
	for covered := 0; covered < total; {
		in.Scroll = covered
		page := in.Render()
		lines := strings.Split(page, "\n")
		for _, line := range lines {
			if layout.Width(line) > in.Width {
				t.Fatalf("overflow: %q", line)
			}
		}
		// Read only visible body rows, excluding chrome and any overlap caused
		// by clamping the final page. Assertions use the rendered output.
		body := lines[4 : len(lines)-2]
		offset := in.ClampScroll()
		for _, line := range body[covered-offset:] {
			read.WriteString(line)
		}
		next := offset + len(body)
		if next <= covered {
			t.Fatal("metadata scrolling stopped before the final row")
		}
		covered = next
	}
	text := strings.Join(strings.Fields(read.String()), "")
	for _, want := range []string{profile, directory, "feature/polish", "Locked:true", "Muted:true"} {
		if !strings.Contains(text, want) {
			t.Fatalf("rendered metadata lost %q: %s", want, text)
		}
	}
}

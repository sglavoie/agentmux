package history

import (
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/tui/layout"
)

func TestSearchListOmitsMetadataRepeatedByFilters(t *testing.T) {
	res := transcript.Result{
		Meta: transcript.SessionMeta{
			Project: "Foo", Profile: "work", Harness: "claude",
			LastAt: time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC),
		},
		Matches: 2, Preview: "needle preview",
	}
	for mask := 0; mask < 8; mask++ {
		t.Run(itoa(mask), func(t *testing.T) {
			in := SearchInput{Results: []transcript.Result{res}, Width: 120}
			parts := []string{"2020-01-02"}
			if mask&1 != 0 {
				in.ProjectChip = "Foo"
			} else {
				parts = append(parts, "Foo")
			}
			var tag []string
			if mask&2 != 0 {
				in.ProfileChip = "work"
			} else {
				tag = append(tag, "work")
			}
			if mask&4 != 0 {
				in.HarnessChip = "claude"
			} else {
				tag = append(tag, "claude")
			}
			if len(tag) > 0 {
				parts = append(parts, "["+strings.Join(tag, "/")+"]")
			}
			parts = append(parts, "(2 matches)", "needle preview")
			want := "  " + strings.Join(parts, "  ") + "\n"
			if got := stripAnsi(in.RenderList(10)); got != want {
				t.Fatalf("row = %q, want %q", got, want)
			}
			for _, width := range []int{15, 30, 50} {
				in.Width = width
				if got := strings.TrimSuffix(in.RenderList(10), "\n"); layout.StyledWidth(got) > width {
					t.Fatalf("row exceeds width %d: %q", width, got)
				}
			}
		})
	}
}

func TestSearchListPreservesNonredundantMetadata(t *testing.T) {
	for _, tc := range []struct {
		name string
		meta transcript.SessionMeta
		want string
	}{
		{"shared profile", transcript.SessionMeta{Project: "Foo", Profile: "work", Profiles: []string{"work", "personal"}, Harness: "claude"}, "[work+1]"},
		{"different display profile", transcript.SessionMeta{Project: "Foo", Profile: "personal", Profiles: []string{"work", "personal"}, Harness: "claude"}, "[personal+1]"},
		{"previous results during search", transcript.SessionMeta{Project: "Bar", Profile: "personal", Harness: "codex"}, "Bar  [personal/codex]"},
		{"no profile", transcript.SessionMeta{Project: "Foo", Harness: "claude"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := SearchInput{
				Results: []transcript.Result{{Meta: tc.meta, Preview: "preview"}}, Width: 120,
				ProjectChip: "Foo", ProfileChip: "work", HarnessChip: "claude",
			}
			got := stripAnsi(in.RenderList(10))
			if !strings.Contains(got, tc.want) || strings.Contains(got, "[]") || strings.Contains(got, "[/") || strings.Contains(got, "/]") {
				t.Fatalf("row = %q, want metadata %q without empty tag components", got, tc.want)
			}
		})
	}
}

package session

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestSessionTableHasAHeaderAndRelativeAges(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	views := []sessionView{
		{Harness: "claude", Profile: "personal-claude", Project: "🐱amx", LastAt: now.Add(-3 * time.Minute), ID: "b03c41a5"},
		{Harness: "codex", Profile: "work", LastAt: now.Add(-50 * time.Hour), ID: "01a1"},
	}
	var out bytes.Buffer
	if err := writeSessionTable(&out, views, now); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "HARNESS") {
		t.Fatalf("table = %q, want a header and two rows", out.String())
	}
	if !strings.Contains(lines[1], "3m ago") || !strings.HasSuffix(lines[1], "b03c41a5") {
		t.Errorf("row 1 = %q", lines[1])
	}
	if !strings.Contains(lines[2], " - ") || !strings.Contains(lines[2], "2d ago") {
		t.Errorf("row 2 = %q, want a placeholder project and a day age", lines[2])
	}
}

func TestSessionTableSaysSoWhenEmpty(t *testing.T) {
	var out bytes.Buffer
	if err := writeSessionTable(&out, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if out.String() != "No indexed sessions match.\n" {
		t.Fatalf("empty table = %q", out.String())
	}
}

func TestFormatAgo(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		at   time.Time
		want string
	}{
		{time.Time{}, "-"},
		{now.Add(-10 * time.Second), "just now"},
		{now.Add(time.Minute), "just now"},
		{now.Add(-59 * time.Minute), "59m ago"},
		{now.Add(-23 * time.Hour), "23h ago"},
		{now.Add(-24 * time.Hour), "1d ago"},
	} {
		if got := formatAgo(tc.at, now); got != tc.want {
			t.Errorf("formatAgo(%v) = %q, want %q", tc.at, got, tc.want)
		}
	}
}

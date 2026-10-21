package session

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/projectresolve"
)

func TestDisplayProjectResolvesRenamesWithoutGuessingAliases(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	project := func(title, profile string) config.Session {
		return config.Session{Title: title, Profile: profile, Directory: dir}
	}
	for _, tc := range []struct {
		name     string
		projects []config.Session
		want     string
	}{
		{"renamed", []config.Session{project("⤴ZoomFlow", "personal")}, "⤴ZoomFlow"},
		{"profile override", []config.Session{project("⤴ZoomFlow", "work")}, "⤴ZoomFlow"},
		{"profile disambiguates", []config.Session{project("Work", "work"), project("⤴ZoomFlow", "personal")}, "⤴ZoomFlow"},
		{"existing alias", []config.Session{project("ZoomFlow", "work"), project("Other", "personal")}, "ZoomFlow"},
		{"ambiguous aliases", []config.Session{project("One", "personal"), project("Two", "personal")}, ""},
		{"shared title", []config.Session{project("New", "work"), project("New", "other")}, "New"},
		{"removed", nil, ""},
		{"different directory", []config.Session{{Title: "Other", Profile: "personal", Directory: t.TempDir()}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := projectMatchContext{resolver: projectresolve.New(tc.projects)}
			rs := RunningSession{Project: "ZoomFlow", Profile: "personal", Directory: dir}
			if got := ctx.displayProject(rs); got != tc.want {
				t.Fatalf("displayProject = %q, want %q", got, tc.want)
			}
		})
	}
}

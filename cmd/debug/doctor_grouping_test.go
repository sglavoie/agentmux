package debug

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
)

func TestDoctorFindingLinesGroupProfilesSharingAHookDetail(t *testing.T) {
	findings := []doctorFinding{
		{Severity: severityWarning, Check: "hooks_unverified", Message: "hooks for personal-claude are unverified: CLI 2 is newer than verified hook contract 1"},
		{Severity: severityWarning, Check: "native_logging", Message: "native logs disabled"},
		{Severity: severityWarning, Check: "hooks_unverified", Message: "hooks for copilot are unverified: CLI 9 is newer than verified hook contract 8"},
		{Severity: severityWarning, Check: "hooks_unverified", Message: "hooks for work-claude are unverified: CLI 2 is newer than verified hook contract 1"},
		{Severity: severityFailure, Check: "hooks", Message: "hooks for work-claude require attention: drifted"},
		{Severity: severityWarning, Check: "hooks_unverified", WindowID: 7, Message: "hooks for pane are unverified: CLI 2 is newer than verified hook contract 1"},
	}
	got := strings.Join(doctorFindingLines(findings, severityWarning), "\n")
	want := strings.Join([]string{
		"- [hooks_unverified] hooks for personal-claude, work-claude are unverified: CLI 2 is newer than verified hook contract 1",
		"- [native_logging] native logs disabled",
		"- [hooks_unverified] hooks for copilot are unverified: CLI 9 is newer than verified hook contract 8",
		"- [hooks_unverified] window 7: hooks for pane are unverified: CLI 2 is newer than verified hook contract 1",
	}, "\n")
	if got != want {
		t.Fatalf("warning lines:\n%s\nwant:\n%s", got, want)
	}
	if got := doctorFindingLines(findings, severityFailure); len(got) != 1 || got[0] != "- [hooks] hooks for work-claude require attention: drifted" {
		t.Fatalf("failure lines = %q", got)
	}
}

func TestDoctorProjectDirectoriesWarnsOnceForEveryUnusableDirectory(t *testing.T) {
	resetDoctorTestState(t)
	present := t.TempDir()
	doctorLoadConfig = func() (*config.Config, error) {
		return &config.Config{Projects: []config.Session{
			{Title: "here", Directory: present},
			{Title: "gone", Directory: filepath.Join(present, "gone")},
			{Title: "also-gone", Directory: filepath.Join(present, "also-gone")},
		}}, nil
	}
	var out bytes.Buffer
	var messages []string
	if err := writeDoctorProjectDirectories(&out, func(format string, args ...any) {
		messages = append(messages, format)
	}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "projects: 2 of 3 have an unusable directory\n" {
		t.Fatalf("text = %q", got)
	}
	if len(messages) != 1 {
		t.Fatalf("findings = %q, want exactly one", messages)
	}

	doctorLoadConfig = func() (*config.Config, error) {
		return &config.Config{Projects: []config.Session{{Title: "here", Directory: present}}}, nil
	}
	out.Reset()
	messages = nil
	if err := writeDoctorProjectDirectories(&out, func(format string, args ...any) { messages = append(messages, format) }); err != nil {
		t.Fatal(err)
	}
	if out.String() != "projects: ok (1 configured)\n" || len(messages) != 0 {
		t.Fatalf("clean projects: text %q, findings %q", out.String(), messages)
	}
}

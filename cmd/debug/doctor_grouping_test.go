package debug

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
)

func TestDoctorVerdictCountsPrintedWarningLines(t *testing.T) {
	warn := func(profile string) doctorFinding {
		return doctorFinding{Severity: severityWarning, Check: "hooks_unverified", Message: "hooks for " + profile + " are unverified: CLI 2 is newer than verified hook contract 1"}
	}
	for _, tc := range []struct {
		name     string
		findings []doctorFinding
		want     string
	}{
		{"none", nil, "overall: clean"},
		{"grouped into one line", []doctorFinding{warn("a"), warn("b"), warn("c")}, "overall: clean (1 warning)"},
		{"grouped plus another", []doctorFinding{warn("a"), warn("b"), {Severity: severityWarning, Check: "native_logging", Message: "off"}}, "overall: clean (2 warnings)"},
		{"failure wins", []doctorFinding{warn("a"), {Severity: severityFailure, Check: "kitty", Message: "down"}}, "overall: issues detected"},
	} {
		if got := doctorVerdict(tc.findings); got != tc.want {
			t.Errorf("%s: doctorVerdict = %q, want %q", tc.name, got, tc.want)
		}
	}
}

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

func TestDoctorFindingLinesGroupContractAheadNotesByVersion(t *testing.T) {
	note := func(profile, cli, verified string) doctorFinding {
		return doctorFinding{Severity: severityInfo, Check: "hooks_contract_ahead", Message: "hooks for " + profile +
			" are ahead of the verified contract: CLI " + cli + " is newer than verified hook contract " + verified + ", and its hook events are arriving"}
	}
	findings := []doctorFinding{note("a-claude", "2", "1"), note("a-codex", "9", "8"), note("b-claude", "2", "1")}
	got := strings.Join(doctorFindingLines(findings, severityInfo), "\n")
	want := strings.Join([]string{
		"- [hooks_contract_ahead] hooks for a-claude, b-claude are ahead of the verified contract: CLI 2 is newer than verified hook contract 1, and its hook events are arriving",
		"- [hooks_contract_ahead] hooks for a-codex are ahead of the verified contract: CLI 9 is newer than verified hook contract 8, and its hook events are arriving",
	}, "\n")
	if got != want {
		t.Fatalf("note lines:\n%s\nwant:\n%s", got, want)
	}
	if got := doctorVerdict(findings); got != "overall: clean" {
		t.Fatalf("doctorVerdict = %q, want notes to leave the verdict clean", got)
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

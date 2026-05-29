package harness

import (
	"os"
	"testing"
)

func TestIsAmxHookEntryHandlesPathWithSpaces(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    bool
	}{
		{"simple basename", "amx hook-indicator Stop", true},
		{"absolute path", "/usr/local/bin/amx hook-indicator Stop", true},
		{"path with spaces", "/Users/foo bar/bin/amx hook-indicator Stop", true},
		{"path containing marker text", "/tmp/agent mux hook-indicator test/bin/amx hook-indicator Stop", true},
		{"windows exe path", `C:\Users\foo\bin\amx.exe hook-indicator Stop`, true},
		{"windows exe path with spaces", `C:\Program Files\Agent Mux\amx.exe hook-indicator Stop`, true},
		{"quoted windows exe path", `"C:\Program Files\Agent Mux\amx.exe" hook-indicator Stop`, true},
		{"powershell call operator", `& 'C:\Program Files\Agent Mux\amx.exe' hook-indicator Stop`, true},
		{"windows exe uppercase", `C:\Users\foo\bin\AMX.EXE hook-indicator Stop`, true},
		{"different binary", "/usr/local/bin/notamx hook-indicator Stop", false},
		{"different windows binary", `C:\Users\foo\bin\notamx.exe hook-indicator Stop`, false},
		{"unrelated path", "/opt/hook-indicator-helper/run --start", false},
		{"missing subcommand", "amx", false},
		{"empty", "", false},
		{"sibling name", "/usr/bin/amxhelper hook-indicator Stop", false},
		{"windows sibling name", `C:\Users\foo\bin\amxhelper.exe hook-indicator Stop`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isAmxHookEntry(hookEntry{Type: "command", Command: tc.command})
			if got != tc.want {
				t.Errorf("isAmxHookEntry(%q) = %v, want %v", tc.command, got, tc.want)
			}
		})
	}
}

func TestIsAmxHookEntryChecksCommandWindows(t *testing.T) {
	got := isAmxHookEntry(hookEntry{
		Type:           "command",
		CommandWindows: `C:\Program Files\Agent Mux\amx.exe hook-indicator Stop`,
	})
	if !got {
		t.Fatalf("isAmxHookEntry() did not recognize CommandWindows amx.exe hook")
	}
}

func TestIsAmxHookEntryRequiresCommandType(t *testing.T) {
	got := isAmxHookEntry(hookEntry{
		Type:    "prompt",
		Command: "amx hook-indicator Stop",
	})
	if got {
		t.Fatalf("isAmxHookEntry() recognized non-command hook as Agent Mux-owned")
	}
}

func TestStripAmxGroupsRemovesPathWithSpaces(t *testing.T) {
	in := map[string][]hookGroup{
		"Stop": {{Hooks: []hookEntry{{
			Type:    "command",
			Command: "/Users/foo bar/bin/amx hook-indicator Stop",
			Async:   boolPtr(true),
		}}}},
		"PreToolUse": {{Hooks: []hookEntry{{
			Type:    "command",
			Command: "/usr/local/bin/user-tool --do-stuff",
		}}}},
	}
	out := stripAmxGroups(in)
	if _, ok := out["Stop"]; ok {
		t.Errorf("Stop hook with spaced amx path should have been stripped; got %+v", out["Stop"])
	}
	if _, ok := out["PreToolUse"]; !ok {
		t.Errorf("user-managed PreToolUse should have survived; got %+v", out)
	}
}

func TestCopilotParseInstalledEventsOnlyReportsAmxHooks(t *testing.T) {
	path := t.TempDir() + "/agent-mux.json"
	data := []byte(`{
  "version": 1,
  "hooks": {
    "sessionEnd": [
      {
        "type": "command",
        "bash": "/usr/local/bin/amx hook-indicator sessionEnd"
      }
    ],
    "notification": [
      {
        "type": "command",
        "bash": "/opt/notamx hook-indicator notification"
      }
    ],
    "preToolUse": [
      {
        "type": "command",
        "powershell": "amx hook-indicator preToolUse"
      }
    ]
  }
}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write hooks file: %v", err)
	}

	got, err := (copilotHookContract{}).ParseInstalledEvents(path)
	if err != nil {
		t.Fatalf("ParseInstalledEvents() err: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ParseInstalledEvents() = %v, want exactly sessionEnd and preToolUse", got)
	}
	for _, want := range []string{"sessionEnd", "preToolUse"} {
		found := false
		for _, event := range got {
			if event == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("ParseInstalledEvents() missing %q: %v", want, got)
		}
	}
	for _, event := range got {
		if event == "notification" {
			t.Fatalf("ParseInstalledEvents() reported third-party hook: %v", got)
		}
	}
}

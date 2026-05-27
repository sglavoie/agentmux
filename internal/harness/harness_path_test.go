package harness

import (
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
		{"different binary", "/usr/local/bin/notamx hook-indicator Stop", false},
		{"unrelated path", "/opt/hook-indicator-helper/run --start", false},
		{"missing subcommand", "amx", false},
		{"empty", "", false},
		{"sibling name", "/usr/bin/amxhelper hook-indicator Stop", false},
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

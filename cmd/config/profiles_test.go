package config

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runConfigProfiles(t *testing.T, body string, terminal bool) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AMX_CONFIG", path)
	t.Setenv("amx_CONFIG", path)
	oldTerminal, oldOut := profilesIsTerminal, configProfilesCmd.OutOrStdout()
	t.Cleanup(func() { profilesIsTerminal = oldTerminal; configProfilesCmd.SetOut(oldOut) })
	profilesIsTerminal = func(io.Writer) bool { return terminal }
	var out bytes.Buffer
	configProfilesCmd.SetOut(&out)
	if err := configProfilesCmd.RunE(configProfilesCmd, nil); err != nil {
		t.Fatalf("config profiles: %v", err)
	}
	return out.String()
}

// A person gets a table, or a sentence when there is nothing to list; a pipe
// keeps the tab-separated rows.
func TestConfigProfilesReadsAsATableInATerminal(t *testing.T) {
	body := validConfigBody(t.TempDir())
	if got := runConfigProfiles(t, body, true); !strings.HasPrefix(got, "PROFILE") || !strings.Contains(got, "work") || strings.Contains(got, "\t") {
		t.Fatalf("terminal output:\n%q", got)
	}
	if got := runConfigProfiles(t, body, false); got != "work\tclaude\n" {
		t.Fatalf("piped output = %q", got)
	}
	empty := "[harness_emojis]\nclaude = \"🦊\"\n"
	if got := runConfigProfiles(t, empty, true); !strings.Contains(got, "No profiles are configured") || !strings.Contains(got, "amx config setup") {
		t.Fatalf("empty terminal output = %q", got)
	}
	if got := runConfigProfiles(t, empty, false); got != "" {
		t.Fatalf("empty piped output = %q", got)
	}
}

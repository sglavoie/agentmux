package session

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/session"
)

// TestNameSessionWritesAndRemovesTheName: the app's workspace names the
// session it shows by runtime session. The name lands on the pane cleaned, an
// empty one removes it, and a session that is not running is an error.
func TestNameSessionWritesAndRemovesTheName(t *testing.T) {
	f, launched := attachLaunchAgent(t)
	id := string(launched.RuntimeSessionID)
	current := ""
	clearName := false
	run := func(named string, args ...string) (string, error) {
		t.Helper()
		oldNamed, oldJSON, oldClear, oldOut, oldCurrent := nameSessionRuntimeSession, nameSessionJSON, nameSessionClear, nameSessionCmd.OutOrStdout(), currentRuntimeSession
		defer func() {
			nameSessionRuntimeSession, nameSessionJSON, nameSessionClear = oldNamed, oldJSON, oldClear
			nameSessionCmd.SetOut(oldOut)
			currentRuntimeSession = oldCurrent
		}()
		currentRuntimeSession = func() string { return current }
		nameSessionRuntimeSession, nameSessionJSON, nameSessionClear = named, true, clearName
		var out bytes.Buffer
		nameSessionCmd.SetOut(&out)
		err := nameSessionCmd.RunE(nameSessionCmd, args)
		return strings.TrimSpace(out.String()), err
	}
	kept := func() string {
		t.Helper()
		panes, err := f.Backend.Discover()
		if err != nil {
			t.Fatal(err)
		}
		for _, pane := range panes {
			if pane.Ref == launched.Pane {
				return pane.Vars[session.NameVar]
			}
		}
		t.Fatal("the launched pane is not listed")
		return ""
	}

	out, err := run(id, "  billing\tfix\n")
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"schema_version":1,"data":{"runtime_session_id":"` + id + `","name":"billing fix","changed":true}}`; out != want {
		t.Fatalf("json = %s\nwant   %s", out, want)
	}
	if got := kept(); got != "billing fix" {
		t.Fatalf("the pane carries the name %q", got)
	}
	if out, err = run(id, "billing fix"); err != nil || !strings.Contains(out, `"name":"billing fix","changed":false`) {
		t.Fatalf("the same name again: out = %s, err = %v", out, err)
	}
	// No name at all is a missing parameter, never a silent removal.
	if out, err = run(id); err == nil || !strings.Contains(out, `"code":"missing_parameter"`) {
		t.Fatalf("no name: out = %s, err = %v", out, err)
	}
	if got := kept(); got != "billing fix" {
		t.Fatalf("a bare name-session changed the name to %q", got)
	}
	// A runtime session ID typed as the name is refused with a hint.
	if out, err = run(id, id); err == nil || !strings.Contains(out, `"code":"invalid_arguments"`) || !strings.Contains(out, "--runtime-session") {
		t.Fatalf("runtime session ID as the name: out = %s, err = %v", out, err)
	}
	// An explicit empty name removes it, as the app asks.
	if out, err = run(id, ""); err != nil || !strings.Contains(out, `"name":"","changed":true`) {
		t.Fatalf("empty name: out = %s, err = %v", out, err)
	}
	if got := kept(); got != "" {
		t.Fatalf("the pane still carries the name %q after it was removed", got)
	}
	if _, err = run(id, "again"); err != nil {
		t.Fatal(err)
	}
	clearName = true
	if out, err = run(id, "x"); err == nil || !strings.Contains(out, `"code":"invalid_arguments"`) {
		t.Fatalf("--clear with a name: out = %s, err = %v", out, err)
	}
	if out, err = run(id); err != nil || !strings.Contains(out, `"name":"","changed":true`) {
		t.Fatalf("--clear: out = %s, err = %v", out, err)
	}
	clearName = false

	// A session named exactly and not running is an error, not a no-op.
	unknown := "rs-" + strings.Repeat("0", 32)
	out, err = run(unknown, "x")
	var exit *cli.ExitError
	if !errors.As(err, &exit) || !strings.Contains(out, `"code":"not_found"`) {
		t.Fatalf("unknown runtime session: out = %s, err = %v", out, err)
	}
	if !strings.Contains(out, "no running tmux session with runtime session "+unknown) {
		t.Fatalf("unknown runtime session message: %s", out)
	}
	if out, err = run("nonsense", "x"); err == nil || !strings.Contains(out, `"code":"invalid_arguments"`) {
		t.Fatalf("malformed runtime session: out = %s, err = %v", out, err)
	}
	if _, err = run("", "x"); err == nil {
		t.Fatal("no runtime session named, yet the command succeeded")
	}

	// With nothing named, the session the command runs in is the one meant.
	current = id
	if out, err = run("", "from inside"); err != nil || !strings.Contains(out, `"name":"from inside","changed":true`) {
		t.Fatalf("current runtime session: out = %s, err = %v", out, err)
	}
	if got := kept(); got != "from inside" {
		t.Fatalf("the pane carries the name %q after naming the current session", got)
	}
}

func TestNameSessionSummaryIsOneLine(t *testing.T) {
	t.Parallel()

	id := "rs-" + strings.Repeat("a", 32)
	for _, tc := range []struct {
		result nameSessionResult
		want   string
	}{
		{nameSessionResult{RuntimeSessionID: id, Name: "x", Changed: true}, `named ` + id + ` "x"`},
		{nameSessionResult{RuntimeSessionID: id, Name: "x"}, id + ` is already named "x"`},
		{nameSessionResult{RuntimeSessionID: id, Changed: true}, "removed name from " + id},
		{nameSessionResult{RuntimeSessionID: id}, id + " has no name"},
	} {
		if got := nameSessionSummary(tc.result); got != tc.want {
			t.Errorf("nameSessionSummary(%+v) = %q; want %q", tc.result, got, tc.want)
		}
	}
}

func TestCleanNameKeepsOneShortLine(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{
		"":                       "",
		" \t\n":                  "",
		"  billing   fix ":       "billing fix",
		"a\x1b[31mb\nc":          "a [31mb c",
		strings.Repeat("é", 100): strings.Repeat("é", session.NameMax),
	} {
		if got := session.CleanName(in); got != want {
			t.Errorf("CleanName(%q) = %q; want %q", in, got, want)
		}
	}
}

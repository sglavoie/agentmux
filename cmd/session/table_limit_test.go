package session

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
)

// A terminal table without --limit stops at sessionTableRows and says how
// many it left out; an explicit --limit, even 0, is taken as given.
func TestTerminalTableDefaultLimit(t *testing.T) {
	t.Parallel()

	newCmd := func() *cobra.Command {
		cmd := &cobra.Command{}
		cmd.Flags().IntP("limit", "n", 0, "")
		return cmd
	}
	if keep, omitted := tableDefaultLimit(newCmd(), sessionTableRows); keep != sessionTableRows || omitted != 0 {
		t.Fatalf("at the cap: keep %d, omitted %d", keep, omitted)
	}
	if keep, omitted := tableDefaultLimit(newCmd(), sessionTableRows+7); keep != sessionTableRows || omitted != 7 {
		t.Fatalf("over the cap: keep %d, omitted %d", keep, omitted)
	}
	explicit := newCmd()
	if err := explicit.Flags().Set("limit", "0"); err != nil {
		t.Fatal(err)
	}
	if keep, omitted := tableDefaultLimit(explicit, 300); keep != 300 || omitted != 0 {
		t.Fatalf("--limit 0: keep %d, omitted %d", keep, omitted)
	}

	var out bytes.Buffer
	if err := writeOmittedFooter(&out, 25, 0, 0); err != nil || out.Len() != 0 {
		t.Fatalf("nothing omitted wrote %q, %v", out.String(), err)
	}
	if err := writeOmittedFooter(&out, 25, 286, 0); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "Showing the 25 most recent of 311; -n N shows more, -n 0 shows all.\n"; got != want {
		t.Fatalf("footer = %q, want %q", got, want)
	}

	// Automated sessions --all would add are counted whether or not the
	// limit cut anything.
	for _, tc := range []struct {
		omitted, hidden int
		want            string
	}{
		{0, 1, "1 automated session is hidden; --all includes it.\n"},
		{0, 85, "85 automated sessions are hidden; --all includes them.\n"},
		{311, 85, "Showing the 25 most recent of 336; -n N shows more, -n 0 shows all.\n85 automated sessions are hidden; --all includes them.\n"},
	} {
		out.Reset()
		if err := writeOmittedFooter(&out, 25, tc.omitted, tc.hidden); err != nil {
			t.Fatal(err)
		}
		if out.String() != tc.want {
			t.Fatalf("footer(%d omitted, %d hidden) = %q, want %q", tc.omitted, tc.hidden, out.String(), tc.want)
		}
	}
}

func TestShellWordQuotesWhatAShellWouldSplit(t *testing.T) {
	t.Parallel()

	for value, want := range map[string]string{
		"copilot":         "copilot",
		"🐱amx":            "🐱amx",
		"Trad-X Pipeline": `"Trad-X Pipeline"`,
		"":                `""`,
	} {
		if got := shellWord(value); got != want {
			t.Errorf("shellWord(%q) = %s, want %s", value, got, want)
		}
	}
}

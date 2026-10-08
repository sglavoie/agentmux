package session

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
)

// A terminal table without --limit stops at sessionTableRows and says how
// many it left out; an explicit --limit, even 0, is taken as given.
func TestTerminalTableDefaultLimit(t *testing.T) {
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
	if err := writeOmittedFooter(&out, 25, 0); err != nil || out.Len() != 0 {
		t.Fatalf("nothing omitted wrote %q, %v", out.String(), err)
	}
	if err := writeOmittedFooter(&out, 25, 286); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "Showing the 25 most recent of 311; -n N shows more, -n 0 shows all.\n"; got != want {
		t.Fatalf("footer = %q, want %q", got, want)
	}
}

func TestShellWordQuotesWhatAShellWouldSplit(t *testing.T) {
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

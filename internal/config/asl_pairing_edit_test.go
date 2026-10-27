package config

// What an operator's file looks like after pairing wrote to it. Every case here
// is really the same assertion: the only thing that changed is what pairing was
// asked to change, and the result still parses.

import (
	"errors"
	"strings"
	"testing"
)

// applied rewrites input and re-parses it, because "the text looks right" and
// "the daemon can still read it" are different claims and only the second one
// matters at startup.
func applied(t *testing.T, input string, pairing ASLPairing) (string, *Config) {
	t.Helper()
	out, err := ApplyASLPairing([]byte(input), pairing)
	if err != nil {
		t.Fatalf("ApplyASLPairing() = %v", err)
	}
	cfg, err := parseConfig(out)
	if err != nil {
		t.Fatalf("the rewritten config no longer parses: %v\n---\n%s", err, out)
	}
	return string(out), cfg
}

// The reason this is a text editor and not a marshaller. A round trip through a
// struct would have deleted all of this.
func TestPairingPreservesCommentsBlankLinesAndKeyOrder(t *testing.T) {
	input := `# Agent Mux configuration.

[defaults]
sort = "alpha"      # keep it stable
refresh_interval = 2

# The bridge. Enabled by hand in 2025 after the incident.
[defaults.asl]
command = "asl"     # resolved on PATH, which is the problem
# state_root is intentionally left to XDG here.

[[profiles]]
name = "work"
harness = "codex"
`
	out, cfg := applied(t, input, ASLPairing{
		Command:   "/Users/op/.local/bin/asl",
		StateRoot: "/Users/op/.local/state/agent-session-loop",
		Required:  true,
	})

	for _, keep := range []string{
		"# Agent Mux configuration.",
		`sort = "alpha"      # keep it stable`,
		"# The bridge. Enabled by hand in 2025 after the incident.",
		"# state_root is intentionally left to XDG here.",
		"[[profiles]]",
		`name = "work"`,
	} {
		if !strings.Contains(out, keep) {
			t.Errorf("the rewrite dropped %q\n---\n%s", keep, out)
		}
	}
	if cfg.Defaults.ASL.Command != "/Users/op/.local/bin/asl" {
		t.Errorf("command = %q", cfg.Defaults.ASL.Command)
	}
	if cfg.Defaults.ASL.StateRoot != "/Users/op/.local/state/agent-session-loop" {
		t.Errorf("state_root = %q", cfg.Defaults.ASL.StateRoot)
	}
	if cfg.Defaults.ASL.ASLModeValue() != "required" {
		t.Errorf("mode = %q, want required", cfg.Defaults.ASL.ASLModeValue())
	}
}

// The comment on the line pairing rewrote explains why the key is set at all.
// Changing the value does not make that explanation wrong.
func TestPairingKeepsTheTrailingCommentOnARewrittenKey(t *testing.T) {
	out, _ := applied(t, "[defaults.asl]\ncommand = \"asl\"  # the paired build\n",
		ASLPairing{Command: "/opt/asl/bin/asl"})
	if !strings.Contains(out, "# the paired build") {
		t.Errorf("the rewrite dropped the trailing comment\n---\n%s", out)
	}
	if !strings.Contains(out, `command = "/opt/asl/bin/asl"`) {
		t.Errorf("the value was not rewritten\n---\n%s", out)
	}
}

// A `#` inside a path is not a comment. Treating it as one would truncate the
// value and produce a file that does not parse.
func TestAHashInsideAPathIsNotAComment(t *testing.T) {
	input := "[defaults.asl]\ncommand = \"/opt/a#b/asl\"\n"
	out, cfg := applied(t, input, ASLPairing{StateRoot: "/srv/state"})
	if cfg.Defaults.ASL.Command != "/opt/a#b/asl" {
		t.Errorf("command = %q, want the untouched path\n---\n%s", cfg.Defaults.ASL.Command, out)
	}
}

// New keys join the table they belong to. Appended to the end of the file they
// would land under whatever header came last and mean something else entirely.
func TestNewKeysAreInsertedInsideTheTableNotAtTheEndOfTheFile(t *testing.T) {
	input := `[defaults.asl]
command = "asl"

[[profiles]]
name = "work"
harness = "codex"
`
	out, cfg := applied(t, input, ASLPairing{Command: "/bin/asl", StateRoot: "/srv/state", Required: true})
	if cfg.Defaults.ASL.StateRoot != "/srv/state" || cfg.Defaults.ASL.ASLModeValue() != "required" {
		t.Fatalf("the new keys did not land in [defaults.asl]\n---\n%s", out)
	}
	stateRootAt := strings.Index(out, "state_root")
	profilesAt := strings.Index(out, "[[profiles]]")
	if stateRootAt < 0 || profilesAt < 0 || stateRootAt > profilesAt {
		t.Errorf("state_root was written after [[profiles]]\n---\n%s", out)
	}
}

// A file with no table at all is the common first-pairing case.
func TestAMissingTableIsAppendedWhole(t *testing.T) {
	input := "[defaults]\nsort = \"alpha\"\n"
	out, cfg := applied(t, input, ASLPairing{Command: "/bin/asl", StateRoot: "/srv/state", Required: true})
	if !strings.Contains(out, "[defaults.asl]") {
		t.Fatalf("no table was written\n---\n%s", out)
	}
	if cfg.Defaults.ASL.Command != "/bin/asl" || cfg.Defaults.ASL.ASLModeValue() != "required" {
		t.Errorf("config = %+v", cfg.Defaults.ASL)
	}
	if !strings.Contains(out, "asl-sync") {
		t.Errorf("the appended table does not say what wrote it\n---\n%s", out)
	}
}

// An empty file is still a config file.
func TestAnEmptyConfigGainsTheTable(t *testing.T) {
	_, cfg := applied(t, "", ASLPairing{Command: "/bin/asl"})
	if cfg.Defaults.ASL.Command != "/bin/asl" {
		t.Errorf("command = %q", cfg.Defaults.ASL.Command)
	}
}

// Two machines paired the same way must produce the same bytes, or a config
// under version control churns on every run.
func TestPairingIsIdempotent(t *testing.T) {
	pairing := ASLPairing{Command: "/bin/asl", StateRoot: "/srv/state", Required: true}
	once, err := ApplyASLPairing([]byte("[defaults]\nsort = \"alpha\"\n"), pairing)
	if err != nil {
		t.Fatalf("first apply: %v", err)
	}
	twice, err := ApplyASLPairing(once, pairing)
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if string(once) != string(twice) {
		t.Errorf("re-pairing changed the file\n--- first ---\n%s\n--- second ---\n%s", once, twice)
	}
}

// An empty field asserts nothing. A caller updating only the command must not
// silently blank the state root someone set deliberately.
func TestEmptyFieldsLeaveExistingValuesAlone(t *testing.T) {
	input := "[defaults.asl]\nenabled = false\ncommand = \"asl\"\nstate_root = \"/srv/state\"\n"
	_, cfg := applied(t, input, ASLPairing{Command: "/bin/asl"})
	if cfg.Defaults.ASL.StateRoot != "/srv/state" {
		t.Errorf("state_root = %q, want it untouched", cfg.Defaults.ASL.StateRoot)
	}
	if cfg.Defaults.ASL.ASLModeValue() != "disabled" {
		t.Errorf("mode = %q; Required was not set, so enabled must not move",
			cfg.Defaults.ASL.ASLModeValue())
	}
}

// Both spellings are valid TOML and mean the same table, but writing a header
// beside a dotted key redefines it and the file stops parsing — at the next
// daemon start, not here. So this is refused, with the edit that fixes it.
func TestDottedKeysUnderDefaultsAreRefusedWithRemediation(t *testing.T) {
	input := "[defaults]\nasl.command = \"asl\"\n"
	_, err := ApplyASLPairing([]byte(input), ASLPairing{Command: "/bin/asl"})
	var ambiguous *ErrASLTableAmbiguous
	if !errors.As(err, &ambiguous) {
		t.Fatalf("ApplyASLPairing() = %v, want an ambiguity refusal", err)
	}
	if !strings.Contains(ambiguous.Remediation, "[defaults.asl]") {
		t.Errorf("remediation %q does not name the edit that fixes it", ambiguous.Remediation)
	}
}

// The same table can be written as an inline table under [defaults] or reached
// from the top of the file. Appending a [defaults.asl] header to any of these
// would redefine it, so each is refused with the edit that makes it editable.
func TestOtherSpellingsOfTheTableAreRefusedWithRemediation(t *testing.T) {
	for name, input := range map[string]string{
		"inline table under defaults": "[defaults]\nasl = { command = \"/old/asl\" }\n",
		"root dotted key":             "defaults.asl.command = \"/old/asl\"\n\n[[profiles]]\nname = \"a\"\n",
		"root dotted table":           "defaults.asl = { command = \"/old/asl\" }\n",
		"root inline defaults":        "defaults = { asl = { command = \"/old/asl\" } }\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ApplyASLPairing([]byte(input), ASLPairing{Command: "/bin/asl"})
			var ambiguous *ErrASLTableAmbiguous
			if !errors.As(err, &ambiguous) {
				t.Fatalf("ApplyASLPairing() = %v, want an ambiguity refusal", err)
			}
			if !strings.Contains(ambiguous.Remediation, "[defaults.asl]") {
				t.Errorf("remediation %q does not name the edit that fixes it", ambiguous.Remediation)
			}
		})
	}
}

// Whatever shape the checks above do not know, the editor never returns bytes
// that fail to parse: here a value spanning several lines is rewritten on its
// first line only, which would leave the rest of it dangling.
func TestAnEditThatWouldNotParseIsRefused(t *testing.T) {
	input := "[defaults.asl]\nstate_root = [\n  \"/srv/state\",\n]\n"
	_, err := ApplyASLPairing([]byte(input), ASLPairing{StateRoot: "/srv/new"})
	var ambiguous *ErrASLTableAmbiguous
	if !errors.As(err, &ambiguous) {
		t.Fatalf("ApplyASLPairing() = %v, want a refusal of an unparsable result", err)
	}
}

// A duplicate key means the file was already invalid. Rewriting one occurrence
// would leave the other and hide that.
func TestADuplicateKeyIsRefusedRatherThanRewrittenTwice(t *testing.T) {
	input := "[defaults.asl]\ncommand = \"asl\"\ncommand = \"other\"\n"
	_, err := ApplyASLPairing([]byte(input), ASLPairing{Command: "/bin/asl"})
	var ambiguous *ErrASLTableAmbiguous
	if !errors.As(err, &ambiguous) {
		t.Fatalf("ApplyASLPairing() = %v, want a duplicate-key refusal", err)
	}
}

// A dotted key under some other table is not this table's problem.
func TestDottedKeysUnderAnotherTableAreIgnored(t *testing.T) {
	input := "[other]\nasl.command = \"asl\"\n\n[defaults.asl]\ncommand = \"asl\"\n"
	out, err := ApplyASLPairing([]byte(input), ASLPairing{Command: "/bin/asl"})
	if err != nil {
		t.Fatalf("ApplyASLPairing() = %v", err)
	}
	if !strings.Contains(string(out), `command = "/bin/asl"`) {
		t.Errorf("the table was not rewritten\n---\n%s", out)
	}
}

// A path with a quote or backslash in it is legal on the filesystems this runs
// on, so it has to survive the round trip rather than corrupt the file.
func TestAwkwardPathsAreEscaped(t *testing.T) {
	awkward := `/opt/we"ird\path/asl`
	_, cfg := applied(t, "[defaults.asl]\n", ASLPairing{Command: awkward})
	if cfg.Defaults.ASL.Command != awkward {
		t.Errorf("command = %q, want %q", cfg.Defaults.ASL.Command, awkward)
	}
}

// A file without a trailing newline should not gain one, and one with a
// trailing newline should not lose it: either way it is diff noise.
func TestTrailingNewlineIsPreserved(t *testing.T) {
	withNewline, err := ApplyASLPairing([]byte("[defaults.asl]\ncommand = \"asl\"\n"), ASLPairing{Command: "/bin/asl"})
	if err != nil {
		t.Fatalf("ApplyASLPairing() = %v", err)
	}
	if !strings.HasSuffix(string(withNewline), "\n") {
		t.Error("a trailing newline was dropped")
	}
	without, err := ApplyASLPairing([]byte("[defaults.asl]\ncommand = \"asl\""), ASLPairing{Command: "/bin/asl"})
	if err != nil {
		t.Fatalf("ApplyASLPairing() = %v", err)
	}
	if strings.HasSuffix(string(without), "\n") {
		t.Error("a trailing newline was added")
	}
}

// An array-of-tables header ends the [defaults.asl] body just as a plain one
// does; missing that would insert keys into the wrong table.
func TestAnArrayOfTablesHeaderEndsTheTable(t *testing.T) {
	input := "[defaults.asl]\ncommand = \"asl\"\n[[projects]]\ntitle = \"API\"\n"
	out, cfg := applied(t, input, ASLPairing{StateRoot: "/srv/state"})
	if cfg.Defaults.ASL.StateRoot != "/srv/state" {
		t.Fatalf("state_root = %q\n---\n%s", cfg.Defaults.ASL.StateRoot, out)
	}
	if strings.Index(out, "state_root") > strings.Index(out, "[[projects]]") {
		t.Errorf("state_root landed inside [[projects]]\n---\n%s", out)
	}
}

// A no-op request must not rewrite the file at all.
func TestAnEmptyPairingReturnsTheFileUnchanged(t *testing.T) {
	input := "[defaults.asl]\ncommand = \"asl\"\n"
	out, err := ApplyASLPairing([]byte(input), ASLPairing{})
	if err != nil {
		t.Fatalf("ApplyASLPairing() = %v", err)
	}
	if string(out) != input {
		t.Errorf("out = %q, want the input unchanged", out)
	}
}

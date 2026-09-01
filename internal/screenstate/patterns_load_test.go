package screenstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Loading a user override: what shadows the embedded set, what is refused, and
// what a refusal leaves installed.

func TestLoadMissingOverrideIsNotAWarning(t *testing.T) {
	restorePatterns(t)

	warnings := Load(t.TempDir())
	if len(warnings) != 0 {
		t.Fatalf("a config dir with no overrides must load silently, got %v", warnings)
	}
	set, ok := PatternsFor("claude")
	if !ok || set.Source() != SourceEmbedded {
		t.Fatalf("claude set = %+v, ok = %v; want the embedded default", set, ok)
	}
}

func TestLoadUnreadableOverrideKeepsEmbedded(t *testing.T) {
	restorePatterns(t)

	dir := t.TempDir()
	// A directory where the override file belongs is an unreadable override on
	// every platform, and unlike a chmod it also fails for root.
	blocked := filepath.Join(dir, patternsDirName, "claude.toml")
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	warnings := Load(dir)
	unreadable := warningsOfKind(warnings, WarnOverrideUnreadable)
	if len(unreadable) != 1 {
		t.Fatalf("warnings = %v, want exactly one %s", warnings, WarnOverrideUnreadable)
	}
	if unreadable[0].Harness != "claude" || unreadable[0].Path != blocked {
		t.Fatalf("warning = %+v, want it to name the claude override path", unreadable[0])
	}
	if !strings.Contains(unreadable[0].String(), "harness=claude") {
		t.Fatalf("String() = %q, want the harness named", unreadable[0].String())
	}
	set, ok := PatternsFor("claude")
	if !ok || set.Source() != SourceEmbedded {
		t.Fatal("an unreadable override must keep the embedded default, never drop the set")
	}
}

func TestLoadInvalidOverrideKeepsEmbedded(t *testing.T) {
	restorePatterns(t)

	dir := writeOverride(t, "claude", "harness = \"claude\"\n[regex]\nbad = \"([\"\n")
	warnings := Load(dir)
	invalid := warningsOfKind(warnings, WarnOverrideInvalid)
	if len(invalid) != 1 {
		t.Fatalf("warnings = %v, want exactly one %s", warnings, WarnOverrideInvalid)
	}
	if invalid[0].Harness != "claude" {
		t.Fatalf("warning = %+v, want harness claude", invalid[0])
	}
	set, ok := PatternsFor("claude")
	if !ok || set.Source() != SourceEmbedded {
		t.Fatal("an invalid override must keep the embedded default")
	}
	if _, ok := PatternsFor("codex"); !ok {
		t.Fatal("one bad override must not disturb another harness")
	}
}

// completeClaudeOverride is the smallest Claude override the loader will
// install: every clause in claudeRequirements and nothing else.
//
// It is deliberately not a copy of the shipped file. What the replacement tests
// need is a set that satisfies the semantic contract while sharing no VALUES
// with the embedded default, so "the override took effect" and "nothing was
// merged" are both observable — hence the yodelling, and hence the two optional
// status-line clauses being left out.
const completeClaudeOverride = `
harness = "claude"

[regex]
composer_line = '(?m)^\s*[│┃]?\s*>\s*$'
choice_line = '(?m)^\s*>\s*\d+\.\s'
working_status_line = '\x{2026}\s*\(\s*\d+[hms]\b'

[phrases]
interrupt_hint = ["press stop to interrupt"]
background_task = ["ctrl+b to"]
blocking_prompt = ["do you want to yodel"]
`

func TestLoadValidOverrideShadowsEmbedded(t *testing.T) {
	restorePatterns(t)

	dir := writeOverride(t, "claude", completeClaudeOverride)
	warnings := Load(dir)
	applied := warningsOfKind(warnings, WarnOverrideApplied)
	if len(applied) != 1 {
		t.Fatalf("warnings = %v, want exactly one %s", warnings, WarnOverrideApplied)
	}
	expectedPath := filepath.Join(dir, patternsDirName, "claude.toml")
	if applied[0].Path != expectedPath {
		t.Fatalf("warning path = %q, want %q", applied[0].Path, expectedPath)
	}

	set, ok := PatternsFor("claude")
	if !ok {
		t.Fatal("claude set missing after a valid override")
	}
	if set.Source() != expectedPath {
		t.Fatalf("source = %q, want the override path", set.Source())
	}
	if !set.Match("blocking_prompt", "Do you want to yodel?", "do you want to yodel?") {
		t.Fatal("the override's clause must be installed")
	}

	// Whole-file replacement, never a merge. The required clauses share names
	// with the embedded default by definition, so the proof is the two OPTIONAL
	// clauses the override omits: if either survives, something merged.
	for _, name := range []string{claudeClauseCompleted, claudeClauseInterrupted} {
		if set.Has(name) {
			t.Fatalf("clause %q leaked from the embedded default; override must replace, not merge", name)
		}
	}
	// And the values, not only the names: the embedded blocking prompts must be
	// gone rather than sitting alongside the override's.
	if set.Match(claudeClauseBlockingPrompt, "Do you want to proceed?", "do you want to proceed?") {
		t.Fatal("an embedded phrase survived the override; replacement must not merge")
	}
	if len(set.RegexNames()) != 3 || len(set.GroupNames()) != 0 || len(set.PhraseNames()) != 3 {
		t.Fatalf("installed set holds clauses the override did not declare: %+v", set)
	}
	if codex, ok := PatternsFor("codex"); !ok || codex.Source() != SourceEmbedded {
		t.Fatal("an override for one harness must not touch another")
	}
	if sources := LoadedPatternSources(); sources["claude"] != expectedPath {
		t.Fatalf("LoadedPatternSources = %v", sources)
	}
}

// TestLoadRejectsSemanticallyIncompleteOverride is the loader half of the
// guard TestClaudeMissingWorkingClauseCannotSettleLiveScreen pins in the
// adapter. The file below decodes perfectly — it is a valid harness declaration
// and one compilable regex — and before requirementsFor it installed cleanly,
// deleting every Claude veto in the process.
func TestLoadRejectsSemanticallyIncompleteOverride(t *testing.T) {
	restorePatterns(t)

	dir := writeOverride(t, "claude", `
harness = "claude"
[regex]
composer_line = '^\s*[│┃]\s*>'
`)
	warnings := Load(dir)
	invalid := warningsOfKind(warnings, WarnOverrideInvalid)
	if len(invalid) != 1 {
		t.Fatalf("warnings = %v, want exactly one %s", warnings, WarnOverrideInvalid)
	}
	if len(warningsOfKind(warnings, WarnOverrideApplied)) != 0 {
		t.Fatalf("warnings = %v; an incomplete override must not report as applied", warnings)
	}
	// The detail is the whole remedy here: unlike a regex syntax error there is
	// nothing in the file to point at, so it must name the missing clauses and
	// say what each one costs.
	for _, want := range []string{claudeClauseWorking, "live-status veto"} {
		if !strings.Contains(invalid[0].Detail, want) {
			t.Errorf("detail = %q, want it to mention %q", invalid[0].Detail, want)
		}
	}
	set, ok := PatternsFor("claude")
	if !ok || set.Source() != SourceEmbedded {
		t.Fatal("an incomplete override must keep the embedded default in force")
	}
}

// TestLoadRejectsOverrideThatMovesAClauseBetweenSections covers the mistake the
// clause KIND exists for. Patterns.Regexp returns nil for a name defined under
// [phrases], so a set that moved working_status_line there has the clause by
// name, matches nothing with it, and would otherwise install.
func TestLoadRejectsOverrideThatMovesAClauseBetweenSections(t *testing.T) {
	restorePatterns(t)

	// Written as an edit of the override that DOES load, so the only difference
	// between this file and an installable one is which section the clause is in.
	var lines []string
	for _, line := range strings.Split(completeClaudeOverride, "\n") {
		if strings.HasPrefix(line, claudeClauseWorking+" = ") {
			continue
		}
		if strings.HasPrefix(line, claudeClauseInterruptHint+" = ") {
			lines = append(lines, claudeClauseWorking+` = ["esc to interrupt"]`)
		}
		lines = append(lines, line)
	}
	body := strings.Join(lines, "\n")
	if strings.Count(body, claudeClauseWorking) != 1 {
		t.Fatalf("the fixture edit did not move exactly one clause:\n%s", body)
	}

	warnings := Load(writeOverride(t, "claude", body))
	invalid := warningsOfKind(warnings, WarnOverrideInvalid)
	if len(invalid) != 1 {
		t.Fatalf("warnings = %v, want exactly one %s", warnings, WarnOverrideInvalid)
	}
	if !strings.Contains(invalid[0].Detail, "[regex]") {
		t.Errorf("detail = %q, want it to name the section the classifier reads", invalid[0].Detail)
	}
	if set, ok := PatternsFor("claude"); !ok || set.Source() != SourceEmbedded {
		t.Fatal("a misfiled clause must keep the embedded default in force")
	}
}

// TestLoadRejectsCodexOverrideWithoutTheComposerSubmatch is the submatch half of
// the contract, and the one place a pattern that MATCHES CORRECTLY is still
// wrong: codexClauses.composerLine reads submatch 1 as the composer's
// remainder, and with no capture group every chevron line hands it an empty
// remainder and reads as an idle composer.
func TestLoadRejectsCodexOverrideWithoutTheComposerSubmatch(t *testing.T) {
	restorePatterns(t)

	// The shipped file with exactly one edit: the composer regex still matches
	// precisely the same lines, and loses only its capture group. Nothing else in
	// the set changes, so a rejection can only be about the submatch.
	const (
		shipped = `composer_line = '^\s*(?:\x{203a}|>)\s*(.*)$'`
		degroup = `composer_line = '^\s*(?:\x{203a}|>)\s*.*$'`
	)
	embedded := embeddedPatternFile(t, "codex")
	if !strings.Contains(embedded, shipped) {
		t.Fatalf("the codex composer clause was rewritten; update this fixture edit:\n%s", embedded)
	}

	warnings := Load(writeOverride(t, "codex", strings.Replace(embedded, shipped, degroup, 1)))
	invalid := warningsOfKind(warnings, WarnOverrideInvalid)
	if len(invalid) != 1 {
		t.Fatalf("warnings = %v, want exactly one %s", warnings, WarnOverrideInvalid)
	}
	if !strings.Contains(invalid[0].Detail, "capture group") {
		t.Errorf("detail = %q, want it to name the submatch contract", invalid[0].Detail)
	}
	if set, ok := PatternsFor("codex"); !ok || set.Source() != SourceEmbedded {
		t.Fatal("a composer clause with no capture group must keep the embedded default")
	}
}

// TestEmbeddedPatternsSatisfyTheirRequirements holds the shipped files to the
// contract the loader holds an override to. Without it a clause renamed in one
// half of the pair would ship as a classifier that cannot refuse — and because
// loadEmbedded SKIPS a set that fails, the symptom would be a harness with no
// patterns at all rather than a readable error.
func TestEmbeddedPatternsSatisfyTheirRequirements(t *testing.T) {
	sets, warnings := loadEmbedded()
	if len(warnings) != 0 {
		t.Fatalf("embedded patterns must satisfy their requirements, got: %v", warnings)
	}
	for _, name := range sortedNames(sets) {
		requirements := requirementsFor(name)
		if len(requirements) == 0 {
			t.Errorf("pattern set %q declares no requirements; every set the classifier "+
				"refuses with needs its fail-closed analysis written down", name)
			continue
		}
		if err := requirements.Validate(sets[name]); err != nil {
			t.Errorf("embedded %s: %v", name, err)
		}
	}
}

func TestLoadRejectsRelativeOverrideDir(t *testing.T) {
	restorePatterns(t)

	warnings := Load("relative/config")
	if len(warningsOfKind(warnings, WarnOverrideDirUnresolved)) != 1 {
		t.Fatalf("warnings = %v, want %s", warnings, WarnOverrideDirUnresolved)
	}
	if set, ok := PatternsFor("claude"); !ok || set.Source() != SourceEmbedded {
		t.Fatal("an unresolvable config dir must still install the embedded defaults")
	}
}

func TestLoadWarningsAreDeterministicallyOrdered(t *testing.T) {
	restorePatterns(t)

	dir := t.TempDir()
	for _, harnessName := range []string{"claude", "codex"} {
		overrideDir := filepath.Join(dir, patternsDirName)
		if err := os.MkdirAll(overrideDir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		body := "harness = \"" + harnessName + "\"\n[phrases]\nx = [\"y\"]\n"
		path := filepath.Join(overrideDir, harnessName+".toml")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	warnings := Load(dir)
	if len(warnings) != 2 {
		t.Fatalf("warnings = %v, want one per harness", warnings)
	}
	if warnings[0].Harness != "claude" || warnings[1].Harness != "codex" {
		t.Fatalf("warnings out of order: %v", warnings)
	}
}

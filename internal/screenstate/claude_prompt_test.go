package screenstate

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/harness"
)

// Synthesized Claude prompt screens for the three kinds no captured fixture
// covers. claude_permission_prompt.txt is a real capture and carries the fourth
// (a create prompt, hence prompt_edit), so it is used verbatim below rather
// than re-modelled here — testdata/ is captures only, by the rule in
// fixture_test.go.
//
// Each is written AGAINST the arms that precede its own in
// classifyClaudePrompt, not merely for its own clause: "would you like to" in
// particular appears under prompts of every kind on real screens and would pull
// any of these onto the elicitation arm, so none of them may carry it.
const (
	claudePromptPermissionScreen = `⏺ Bash(rm -rf ./build)
  ⎿  Removes the build directory

 Do you want to proceed?
 ❯ 1. Yes
   2. No, and tell Claude what to do differently

 Esc to cancel
`

	claudePromptTrustScreen = `╭──────────────────────────────────────────────────╮
│ Do you trust the files in this folder?             │
│ /Users/dev/projects/api                            │
╰──────────────────────────────────────────────────╯

 ❯ 1. Yes, proceed
   2. No, exit

 Enter to confirm
`

	claudePromptElicitationScreen = `⏺ I can take two routes here.

 Would you like to run the migration now or review the plan first?
 ❯ 1. Run it
   2. Review the plan

 Enter to confirm
`
)

// claudePreSplitBlockingPrompts is the flat six-phrase blocking_prompt list
// patterns_claude.toml shipped before the kind split, verbatim. It is the
// "before" side of every compatibility and regression assertion in this file,
// and it is a frozen literal on purpose: it models a user override written
// against the previous release, which by definition never moves again.
var claudePreSplitBlockingPrompts = []string{
	"do you want to proceed",
	"do you want to make this edit",
	"do you want to create",
	"do you want to allow",
	"do you trust",
	"would you like to",
}

// TestClaudePromptKindPerSubClause runs every kind through the classifier: the
// real capture for prompt_edit, and one synthesized screen for each of the
// three kinds no capture covers.
//
// It is self-validating for the synthesized screens the same way the Codex kind
// table is: a screen that satisfies an earlier arm lands on the wrong kind and
// fails here, so passing is the evidence that each screen is accepted as the
// kind it was written for and not merely that some clause matched.
func TestClaudePromptKindPerSubClause(t *testing.T) {
	embedded := embeddedClaudePatterns(t)

	cases := []struct {
		name   string
		screen string
		clause string
		want   ClaudePromptKind
	}{
		{
			name:   "edit",
			screen: readScreenFixture(t, "claude_permission_prompt.txt"),
			clause: claudeClausePromptEdit,
			want:   ClaudePromptEdit,
		},
		{
			name:   "permission",
			screen: claudePromptPermissionScreen,
			clause: claudeClausePromptPermission,
			want:   ClaudePromptPermission,
		},
		{
			name:   "trust",
			screen: claudePromptTrustScreen,
			clause: claudeClausePromptTrust,
			want:   ClaudePromptTrust,
		},
		{
			name:   "elicitation",
			screen: claudePromptElicitationScreen,
			clause: claudeClausePromptElicitation,
			want:   ClaudePromptElicitation,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			lower := strings.ToLower(testCase.screen)

			// The clause itself, so a kind that resolved through some other
			// clause cannot pass for this one.
			if !embedded.Match(testCase.clause, testCase.screen, lower) {
				t.Fatalf("clause %q does not match its own screen", testCase.clause)
			}
			kind, ok := classifyClaudePrompt(embedded, testCase.screen, lower)
			if !ok {
				t.Fatalf("classifyClaudePrompt reported no kind for the %s screen", testCase.name)
			}
			if kind != testCase.want {
				t.Fatalf("kind = %q, want %q", kind, testCase.want)
			}

			// The refinement only ever refines: the veto it labels must still
			// fire, and the screen must still read as Blocked.
			got := claudeAdapter{Patterns: embedded}.Classify(testCase.screen)
			if got.Vetoes&VetoBlockingPrompt == 0 {
				t.Fatalf("vetoes = %s, want VetoBlockingPrompt", got.Vetoes)
			}
			if got.State != Blocked {
				t.Fatalf("State = %s, want blocked", got.State)
			}
		})
	}
}

// TestClaudePromptKindAbsentWithoutAPrompt pins the negative direction: an
// ordinary screen names no kind. Without it every assertion above would pass
// against a classifier that answered the same kind for everything.
func TestClaudePromptKindAbsentWithoutAPrompt(t *testing.T) {
	embedded := embeddedClaudePatterns(t)
	for _, name := range []string{
		"claude_idle_completed.txt",
		"claude_working_pouncing_t0.txt",
		"claude_interrupted.txt",
	} {
		screen := readScreenFixture(t, name)
		if kind, ok := classifyClaudePrompt(embedded, screen, strings.ToLower(screen)); ok {
			t.Errorf("%s classified as %q, want no kind", name, kind)
		}
	}
}

// TestClaudeFlatBlockingPromptOverrideStillVetoes is the compatibility test,
// and it is the one this whole split has to earn.
//
// It writes the pattern file this release's predecessor shipped — flat
// blocking_prompt, no kind clauses, no group — as a user override, LOADS it
// through the real loader, and then asserts the four things such a user is
// entitled to: the override installs, it satisfies claudeRequirements, a
// permission prompt still raises VetoBlockingPrompt and still reads as Blocked,
// and the only thing lost is the label.
//
// It goes through Load rather than Decode because the loader is where the
// failure would actually land: adding the kind clauses to claudeRequirements
// would make this file be REFUSED at install time, with a message naming
// clauses the user never wrote.
func TestClaudeFlatBlockingPromptOverrideStillVetoes(t *testing.T) {
	restorePatterns(t)

	body := claudePreSplitPatternFile(t)
	dir := writeOverride(t, harness.Claude, body)
	warnings := Load(dir)
	if applied := warningsOfKind(warnings, WarnOverrideApplied); len(applied) != 1 {
		t.Fatalf("warnings = %v, want exactly one %s; the pre-split file must still install",
			warnings, WarnOverrideApplied)
	}

	set, ok := PatternsFor(harness.Claude)
	if !ok {
		t.Fatal("no claude set after loading the pre-split override")
	}
	if set.Source() == SourceEmbedded {
		t.Fatal("the override did not take: the embedded set is still installed")
	}
	// The shape is the point: a flat phrase list, not a group.
	if len(set.Phrases(claudeClauseBlockingPrompt)) != len(claudePreSplitBlockingPrompts) {
		t.Fatalf("blocking_prompt phrases = %v, want the flat pre-split list",
			set.Phrases(claudeClauseBlockingPrompt))
	}
	if _, isGroup := set.Group(claudeClauseBlockingPrompt); isGroup {
		t.Fatal("blocking_prompt decoded as a group; this test is no longer about a flat list")
	}
	for _, name := range []string{
		claudeClausePromptPermission, claudeClausePromptTrust,
		claudeClausePromptEdit, claudeClausePromptElicitation,
	} {
		if set.Has(name) {
			t.Fatalf("the pre-split override defines %q; it must not, or nothing is being proved", name)
		}
	}

	// 1. It satisfies the requirements — which is what the loader enforces, and
	//    what adding the kind clauses to claudeRequirements would break.
	if err := claudeRequirements.Validate(set); err != nil {
		t.Fatalf("claudeRequirements.Validate = %v, want nil; the kind clauses must stay optional", err)
	}

	// 2. It still vetoes, and still reads as blocked.
	screen := readScreenFixture(t, "claude_permission_prompt.txt")
	got := claudeAdapter{Patterns: set}.Classify(screen)
	if got.Vetoes&VetoBlockingPrompt == 0 {
		t.Fatalf("vetoes = %s, want VetoBlockingPrompt", got.Vetoes)
	}
	if got.State != Blocked {
		t.Fatalf("State = %s, want blocked", got.State)
	}
	if got.SettleableTerminal() {
		t.Fatal("a visible permission prompt settled against the pre-split override")
	}

	// 3. And the only casualty is the label. ClassifyClaudePrompt reads the
	//    INSTALLED set, so this is the production path, not a hand-passed one.
	if kind, ok := ClassifyClaudePrompt(screen); ok {
		t.Fatalf("kind = %q, want none; a flat-list override defines no kind clause", kind)
	}
}

// TestClaudeVetoesUnchangedBySplit is the regression the split needs: across
// every captured Claude screen, plus the three synthesized prompt screens, the
// veto set the embedded (post-split) file produces must equal the veto set the
// pre-split flat list produced — bit for bit, and with the observation's state
// and tier alongside it, since a veto set is only half of what a caller reads.
//
// The "before" set is rebuilt from the shipped file's own clauses with only
// blocking_prompt swapped back to the flat list (see
// claudePreSplitPatternFile), so this compares the two blocking_prompt shapes
// and nothing else. A later edit to some other clause changes both sides
// equally and cannot make this test lie.
func TestClaudeVetoesUnchangedBySplit(t *testing.T) {
	after := embeddedClaudePatterns(t)
	before, err := decodePatterns([]byte(claudePreSplitPatternFile(t)), harness.Claude)
	if err != nil {
		t.Fatalf("decode the pre-split file: %v", err)
	}

	screens := map[string]string{
		"synthesized/permission":  claudePromptPermissionScreen,
		"synthesized/trust":       claudePromptTrustScreen,
		"synthesized/elicitation": claudePromptElicitationScreen,
	}
	for _, name := range claudeFixtureNames(t) {
		screens["testdata/"+name] = readScreenFixture(t, name)
	}
	names := make([]string, 0, len(screens))
	for name := range screens {
		names = append(names, name)
	}
	sort.Strings(names)

	blocked := 0
	for _, name := range names {
		screen := screens[name]
		beforeObs := claudeAdapter{Patterns: before}.Classify(screen)
		afterObs := claudeAdapter{Patterns: after}.Classify(screen)
		if beforeObs.Vetoes != afterObs.Vetoes {
			t.Errorf("%s: vetoes %s → %s; the split changed a refusal",
				name, beforeObs.Vetoes, afterObs.Vetoes)
		}
		if beforeObs.State != afterObs.State || beforeObs.Tier != afterObs.Tier {
			t.Errorf("%s: %s/%s → %s/%s; the split changed a conclusion",
				name, beforeObs.State, beforeObs.Tier, afterObs.State, afterObs.Tier)
		}
		if afterObs.Vetoes&VetoBlockingPrompt != 0 {
			blocked++
		}
	}
	// Equality is trivially satisfiable by a corpus in which nothing is blocked.
	if blocked == 0 {
		t.Fatal("no screen in the corpus raised VetoBlockingPrompt; this test proves nothing")
	}
}

// TestClaudeAndCodexPromptKindsDoNotCollide guards the one property that makes
// a single bare-string state.WindowState.PromptKind workable across two
// harnesses: the union of the two kind sets must be a set of DISTINCT strings,
// or a renderer glossing a kind would have to know which harness wrote it to
// know what it means.
func TestClaudeAndCodexPromptKindsDoNotCollide(t *testing.T) {
	seen := make(map[string]string, 9)
	for _, kind := range []ClaudePromptKind{
		ClaudePromptPermission, ClaudePromptTrust, ClaudePromptEdit, ClaudePromptElicitation,
	} {
		seen[string(kind)] = "claude"
	}
	for _, kind := range []CodexPromptKind{
		CodexPromptEditApproval, CodexPromptRequestUserInput, CodexPromptUnansweredConfirmation,
		CodexPromptCommandApproval, CodexPromptPermissionGrant,
	} {
		if owner, clash := seen[string(kind)]; clash {
			t.Errorf("codex kind %q collides with a %s kind", kind, owner)
		}
		seen[string(kind)] = "codex"
	}
	if len(seen) != 9 {
		t.Fatalf("the two kind sets hold %d distinct strings, want 9", len(seen))
	}
}

// claudePreSplitPatternFile renders the Claude pattern file this release's
// predecessor shipped: every clause the CURRENT embedded file defines, except
// that the four prompt-kind lists and the blocking_prompt group are replaced by
// the flat six-phrase list.
//
// It is rebuilt from the installed set's own accessors rather than pasted as a
// frozen literal so that it tracks every future edit to the clauses this
// session did not touch. A frozen copy would drift, and the drift would show up
// as a failure in TestClaudeVetoesUnchangedBySplit blamed on the split.
func claudePreSplitPatternFile(t *testing.T) string {
	t.Helper()
	set := embeddedClaudePatterns(t)

	// blocking_prompt is the group the split added and the one replaced below;
	// every other group is carried over as shipped.
	if _, ok := set.Group(claudeClauseBlockingPrompt); !ok {
		t.Fatalf("embedded claude groups = %v, want %q among them; this helper needs updating",
			set.GroupNames(), claudeClauseBlockingPrompt)
	}

	var body strings.Builder
	fmt.Fprintf(&body, "harness = %q\n\n[regex]\n", harness.Claude)
	for _, name := range set.RegexNames() {
		source := set.Regexp(name).String()
		if strings.ContainsAny(source, "'\n") {
			t.Fatalf("regex %q contains a quote or newline; the literal-string emit below is unsafe", name)
		}
		fmt.Fprintf(&body, "%s = '%s'\n", name, source)
	}

	body.WriteString("\n[phrases]\n")
	skip := map[string]struct{}{
		claudeClausePromptPermission:  {},
		claudeClausePromptTrust:       {},
		claudeClausePromptEdit:        {},
		claudeClausePromptElicitation: {},
	}
	for _, name := range set.PhraseNames() {
		if _, dropped := skip[name]; dropped {
			continue
		}
		writeClaudePhraseList(&body, name, set.Phrases(name))
	}
	writeClaudePhraseList(&body, claudeClauseBlockingPrompt, claudePreSplitBlockingPrompts)

	for _, name := range set.GroupNames() {
		if name == claudeClauseBlockingPrompt {
			continue
		}
		group, _ := set.Group(name)
		fmt.Fprintf(&body, "\n[group.%s]\n", name)
		if len(group.All) > 0 {
			writeClaudePhraseList(&body, "all", group.All)
		}
		if len(group.Any) > 0 {
			writeClaudePhraseList(&body, "any", group.Any)
		}
	}
	return body.String()
}

func writeClaudePhraseList(body *strings.Builder, name string, phrases []string) {
	fmt.Fprintf(body, "%s = [", name)
	for i, phrase := range phrases {
		if i > 0 {
			body.WriteString(", ")
		}
		fmt.Fprintf(body, "%q", phrase)
	}
	body.WriteString("]\n")
}

// claudeFixtureNames lists every captured Claude screen under testdata/. It is
// read from the directory rather than written out so a fixture added later is
// covered by the regression above without anyone remembering to add it.
func claudeFixtureNames(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(fixtureRoot)
	if err != nil {
		t.Fatalf("read %s: %v", fixtureRoot, err)
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), "claude_") {
			names = append(names, entry.Name())
		}
	}
	if len(names) == 0 {
		t.Fatalf("no claude fixtures under %s", fixtureRoot)
	}
	return names
}

// embeddedClaudePatterns is the installed Claude set, asserted to be the
// embedded one. Every test above compares against the shipped file, so a run
// that somehow left an override installed must fail rather than pass vacuously.
func embeddedClaudePatterns(t *testing.T) Patterns {
	t.Helper()
	set, ok := PatternsFor(harness.Claude)
	if !ok {
		t.Fatal("PatternsFor(claude) returned no set")
	}
	if set.Source() != SourceEmbedded {
		t.Fatalf("Source() = %q, want %q", set.Source(), SourceEmbedded)
	}
	return set
}

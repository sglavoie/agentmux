package dashboard

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/session"
)

// collidingPair is the case the disambiguator exists for: one configured project
// opened twice, so both rows render the same title. Everything the tests below
// vary is which fact separates the two.
func collidingPair() []session.RunningSession {
	return []session.RunningSession{
		{WindowID: 11, Project: "API", Base: "api", Directory: "/repo/api", Branch: "main", Profile: "work"},
		{WindowID: 12, Project: "API", Base: "api", Directory: "/worktrees/api-fix", Branch: "amx/fix", Profile: "work"},
	}
}

// TestNameStandsAloneWithoutACollision pins the quiet case, which is the common
// one: a qualifier is an explanation, and a row with nothing to be confused with
// has nothing to explain.
func TestNameStandsAloneWithoutACollision(t *testing.T) {
	live := []session.RunningSession{
		{WindowID: 11, Project: "API", Directory: "/repo/api", Branch: "main", Profile: "work"},
		{WindowID: 12, Project: "Web", Directory: "/repo/web", Branch: "main", Profile: "work"},
	}
	names := NewNameDisambiguator(live)
	for _, s := range live {
		if got := names.Qualifier(s); got != "" {
			t.Errorf("Qualifier(%q) = %q, want \"\": nothing else on screen is called that", SessionDisplayTitle(s), got)
		}
		if got, want := names.Label(s), SessionDisplayTitle(s); got != want {
			t.Errorf("Label = %q, want the bare title %q", got, want)
		}
	}
}

// TestDuplicateCheckoutsAreToldApartByBranch is the headline case. Managed
// worktrees make "two rows in one repository" ordinary, and they agree on their
// project title, their profile, their harness and their directory PREFIX — the
// branch is the fact that separates them, and it is the fact the operator is
// already thinking in.
func TestDuplicateCheckoutsAreToldApartByBranch(t *testing.T) {
	rows := collidingPair()
	names := NewNameDisambiguator(rows)

	if got, want := names.Qualifier(rows[0]), "main"; got != want {
		t.Errorf("Qualifier(row 0) = %q, want %q", got, want)
	}
	if got, want := names.Qualifier(rows[1]), "amx/fix"; got != want {
		t.Errorf("Qualifier(row 1) = %q, want %q", got, want)
	}
	if got, want := names.Label(rows[1]), "API (amx/fix)"; got != want {
		t.Errorf("Label(row 1) = %q, want %q", got, want)
	}
}

// TestUnavailableGitInformationFallsBackToTheWorktreeFolder covers the whole
// class internal/gitstate reports as an empty branch: a non-repository,
// `git_probe = false`, a detached HEAD, a probe that timed out or has not run
// yet, and a dashboard with no daemon behind it. The rows are still in different
// checkouts and the folder still says which.
func TestUnavailableGitInformationFallsBackToTheWorktreeFolder(t *testing.T) {
	rows := collidingPair()
	rows[0].Branch = ""
	rows[1].Branch = ""
	names := NewNameDisambiguator(rows)

	if got, want := names.Qualifier(rows[0]), "api"; got != want {
		t.Errorf("Qualifier(row 0) = %q, want the directory leaf %q", got, want)
	}
	if got, want := names.Qualifier(rows[1]), "api-fix"; got != want {
		t.Errorf("Qualifier(row 1) = %q, want the directory leaf %q", got, want)
	}
}

// TestOneMissingBranchDoesNotLeaveASiblingBare is the non-emptiness half of the
// level rule. Row 0 has a branch and row 1 does not, so the branch level would
// "separate" the pair by giving one row context and the other nothing — leaving a
// bare `API` beside `API (main)`, which reads as the only one of its kind. The
// level is refused and both rows get the same kind of fact instead.
func TestOneMissingBranchDoesNotLeaveASiblingBare(t *testing.T) {
	rows := collidingPair()
	rows[1].Branch = ""
	names := NewNameDisambiguator(rows)

	if got, want := names.Qualifier(rows[0]), "api"; got != want {
		t.Errorf("Qualifier(row 0) = %q, want the directory leaf %q: the branch level cannot separate a pair only one row has a branch for", got, want)
	}
	if got, want := names.Qualifier(rows[1]), "api-fix"; got != want {
		t.Errorf("Qualifier(row 1) = %q, want the directory leaf %q", got, want)
	}
}

// TestMultipleProfilesAreToldApartByProfile covers one project opened under two
// launch identities in the same checkout. No checkout fact separates them; the
// profile does.
func TestMultipleProfilesAreToldApartByProfile(t *testing.T) {
	rows := collidingPair()
	rows[1].Directory = rows[0].Directory
	rows[1].Branch = rows[0].Branch
	rows[1].Profile = "review"
	names := NewNameDisambiguator(rows)

	if got, want := names.Qualifier(rows[0]), "work"; got != want {
		t.Errorf("Qualifier(row 0) = %q, want %q", got, want)
	}
	if got, want := names.Qualifier(rows[1]), "review"; got != want {
		t.Errorf("Qualifier(row 1) = %q, want %q", got, want)
	}
}

// TestLegacyAccountStandsInForAProfile pins that the profile level reads the
// effective launch identity rather than the field. A session discovered from the
// deprecated account user var carries Account and no Profile, and it is still the
// same fact to a reader.
func TestLegacyAccountStandsInForAProfile(t *testing.T) {
	rows := collidingPair()
	rows[1].Directory = rows[0].Directory
	rows[1].Branch = rows[0].Branch
	rows[0].Profile, rows[0].Account = "", "work"
	rows[1].Profile, rows[1].Account = "", "review"
	names := NewNameDisambiguator(rows)

	if got, want := names.Qualifier(rows[1]), "review"; got != want {
		t.Errorf("Qualifier(row 1) = %q, want %q from the legacy account field", got, want)
	}
}

// TestCheckoutAndProfileCombineWhenNeitherSeparatesAlone is the level neither
// single fact can reach: two branches across two profiles, with each branch and
// each profile used twice. Escalating straight to the window ID here would throw
// away two readable facts in favour of a number that means nothing on sight.
func TestCheckoutAndProfileCombineWhenNeitherSeparatesAlone(t *testing.T) {
	rows := []session.RunningSession{
		{WindowID: 11, Project: "API", Directory: "/repo/api", Branch: "main", Profile: "work"},
		{WindowID: 12, Project: "API", Directory: "/repo/api", Branch: "main", Profile: "review"},
		{WindowID: 13, Project: "API", Directory: "/wt/fix", Branch: "amx/fix", Profile: "work"},
		{WindowID: 14, Project: "API", Directory: "/wt/fix", Branch: "amx/fix", Profile: "review"},
	}
	names := NewNameDisambiguator(rows)

	want := []string{
		"main" + qualifierSeparator + "work",
		"main" + qualifierSeparator + "review",
		"amx/fix" + qualifierSeparator + "work",
		"amx/fix" + qualifierSeparator + "review",
	}
	for i, s := range rows {
		if got := names.Qualifier(s); got != want[i] {
			t.Errorf("Qualifier(row %d) = %q, want %q", i, got, want[i])
		}
	}
}

// TestStableIdentifierIsTheLastResort covers rows that agree on every readable
// fact — the same project reopened twice in the same checkout under the same
// profile. The window ID explains nothing, which is why it is last, but it is
// exact and it is not invented.
func TestStableIdentifierIsTheLastResort(t *testing.T) {
	rows := collidingPair()
	rows[1].Directory = rows[0].Directory
	rows[1].Branch = rows[0].Branch
	names := NewNameDisambiguator(rows)

	if got, want := names.Qualifier(rows[0]), "#11"; got != want {
		t.Errorf("Qualifier(row 0) = %q, want %q", got, want)
	}
	if got, want := names.Label(rows[1]), "API (#12)"; got != want {
		t.Errorf("Label(row 1) = %q, want %q", got, want)
	}
}

// TestUnseparableCollisionAdmitsItRatherThanInventing is the fail-honest end of
// the escalation. Two rows with no branch, no directory, no profile and no window
// ID are two rows this package cannot tell apart, and a qualifier that claimed
// otherwise — an ordinal, a position in the list, a clock reading — would be a
// label the operator could act on that names nothing real.
func TestUnseparableCollisionAdmitsItRatherThanInventing(t *testing.T) {
	rows := []session.RunningSession{
		{Base: "api"},
		{Base: "api"},
	}
	names := NewNameDisambiguator(rows)
	for i, s := range rows {
		if got := names.Qualifier(s); got != "" {
			t.Errorf("Qualifier(row %d) = %q; nothing separates these rows, so nothing may be claimed", i, got)
		}
	}
}

// TestFilteringDoesNotRenameTheRowsThatRemain is the reason collisions are
// measured across the live set. A filter is a lens, and a lens that renamed the
// rows under it would make a session's name depend on what else the operator
// happened to be looking at — the row would rename itself as chips came and went.
func TestFilteringDoesNotRenameTheRowsThatRemain(t *testing.T) {
	live := append(collidingPair(), session.RunningSession{
		WindowID: 13, Project: "Web", Directory: "/repo/web", Branch: "main", Profile: "work",
	})
	filter := Parse("api")
	filtered := ApplyFilter(live, filter, nil, time.Minute)
	if len(filtered) != 2 {
		t.Fatalf("the fixture filtered to %d rows, want the colliding pair", len(filtered))
	}

	full := BuildQueue(QueueInput{Rows: live, Live: live, IdleThreshold: time.Minute})
	narrowed := BuildQueue(QueueInput{Rows: filtered, Live: live, IdleThreshold: time.Minute})
	for i, row := range narrowed {
		if got, want := row.Label(), full[i].Label(); got != want {
			t.Errorf("row %d is %q under a filter and %q without one", i, got, want)
		}
	}

	// The other direction: a filter that keeps only ONE of the colliding pair must
	// keep its qualifier, because the session it is being distinguished from is
	// still live and still one keystroke away.
	single := BuildQueue(QueueInput{Rows: filtered[1:], Live: live, IdleThreshold: time.Minute})
	if got, want := single[0].Qualifier, "amx/fix"; got != want {
		t.Errorf("the surviving row's qualifier = %q, want %q: the row it differs from is still live", got, want)
	}
}

// TestQualifiersAreDisplayOnly pins the boundary the redesign's brief states
// outright: display labels must not become lookup keys. A composed label is not
// matchable and is not what a cursor identity travels on.
func TestQualifiersAreDisplayOnly(t *testing.T) {
	rows := collidingPair()
	names := NewNameDisambiguator(rows)
	label := names.Label(rows[1])

	if got := ApplyFilter(rows, Parse(label), nil, time.Minute); len(got) != 0 {
		t.Errorf("filtering by the composed label %q matched %d rows; the label is presentation and nothing indexes it", label, len(got))
	}
	if got := ApplyFilter(rows, Parse("api"), nil, time.Minute); len(got) != 2 {
		t.Errorf("filtering by the raw title matched %d rows, want 2: the filter still reads the unqualified title", len(got))
	}
	id := CaptureCursorIdentity(rows[1])
	if id.Base == label {
		t.Error("the cursor identity captured the composed label; restoration must travel on the raw identity fields")
	}
	if id.Base != rows[1].Base {
		t.Errorf("CursorIdentity.Base = %q, want the raw base %q", id.Base, rows[1].Base)
	}
}

// TestNameCollisionsAreCaseSensitive keeps this rule distinct from
// CanonicalProjectLabels', which folds case because it aggregates one project's
// rows under one heading. Here the question is whether two LINES look alike, and
// a reader who can see the difference does not need it explained.
func TestNameCollisionsAreCaseSensitive(t *testing.T) {
	rows := []session.RunningSession{
		{WindowID: 11, Project: "api", Directory: "/repo/api", Branch: "main"},
		{WindowID: 12, Project: "API", Directory: "/wt/api", Branch: "amx/fix"},
	}
	names := NewNameDisambiguator(rows)
	for i, s := range rows {
		if got := names.Qualifier(s); got != "" {
			t.Errorf("Qualifier(row %d) = %q; %q and %q are different words on screen", i, got, rows[0].Project, rows[1].Project)
		}
	}
}

// TestNameDisambiguatorIsDeterministic guards the one nondeterminism the
// implementation could have had. Collisions are grouped in a map, and a level
// chosen by iteration order rather than by the fixed escalation would produce a
// queue that renamed its rows between two renders of the same sessions.
func TestNameDisambiguatorIsDeterministic(t *testing.T) {
	rows := []session.RunningSession{
		{WindowID: 11, Project: "API", Directory: "/repo/api", Branch: "main", Profile: "work"},
		{WindowID: 12, Project: "API", Directory: "/wt/fix", Branch: "amx/fix", Profile: "review"},
		{WindowID: 13, Project: "Web", Directory: "/repo/web", Branch: "main", Profile: "work"},
		{WindowID: 14, Project: "Web", Directory: "/repo/web", Branch: "main", Profile: "review"},
	}
	first := NewNameDisambiguator(rows)
	for attempt := 0; attempt < 50; attempt++ {
		again := NewNameDisambiguator(rows)
		for i, s := range rows {
			if got, want := again.Qualifier(s), first.Qualifier(s); got != want {
				t.Fatalf("attempt %d row %d: qualifier %q != %q", attempt, i, got, want)
			}
		}
	}
	// The two collisions escalate to different levels, which is what makes the
	// per-group decision worth pinning: a shared level would hide the difference.
	if got, want := first.Qualifier(rows[0]), "main"; got != want {
		t.Errorf("API rows separate by branch: qualifier = %q, want %q", got, want)
	}
	if got, want := first.Qualifier(rows[3]), "review"; got != want {
		t.Errorf("Web rows share a checkout and separate by profile: qualifier = %q, want %q", got, want)
	}
}

// TestZeroNameDisambiguatorQualifiesNothing pins the zero value, which is what a
// caller with no live set gets: a dashboard that cannot see the other sessions has
// no evidence that any name collides.
func TestZeroNameDisambiguatorQualifiesNothing(t *testing.T) {
	var names NameDisambiguator
	s := collidingPair()[0]
	if got := names.Qualifier(s); got != "" {
		t.Errorf("Qualifier on the zero disambiguator = %q, want \"\"", got)
	}
	if got, want := names.Label(s), "API"; got != want {
		t.Errorf("Label on the zero disambiguator = %q, want %q", got, want)
	}
}

// TestDirectoryLeafRefusesPathsThatNameNoFolder covers the paths a qualifier must
// not be built from: a leaf reading `/` or `.` would be a label that says nothing
// while looking like it says something.
func TestDirectoryLeafRefusesPathsThatNameNoFolder(t *testing.T) {
	cases := map[string]string{
		"":                   "",
		"/":                  "",
		".":                  "",
		"..":                 "",
		"/repo/api":          "api",
		"/repo/api/":         "api",
		"/worktrees/api-fix": "api-fix",
		"relative/dir":       "dir",
	}
	for dir, want := range cases {
		if got := directoryLeaf(dir); got != want {
			t.Errorf("directoryLeaf(%q) = %q, want %q", dir, got, want)
		}
	}
}

// TestQualifierAppliedToAnUnknownSessionStaysQuiet is the one way a colliding
// name can reach a missing fact: a disambiguator applied to a session that was not
// in the live set it was built from. It answers with no qualifier rather than with
// a half-composed one.
func TestQualifierAppliedToAnUnknownSessionStaysQuiet(t *testing.T) {
	names := NewNameDisambiguator(collidingPair())
	stranger := session.RunningSession{Project: "API"}
	if got := names.Qualifier(stranger); got != "" {
		t.Errorf("Qualifier(stranger) = %q, want \"\": it has no branch to be named by", got)
	}
	if got, want := names.Label(stranger), "API"; got != want {
		t.Errorf("Label(stranger) = %q, want %q", got, want)
	}
}

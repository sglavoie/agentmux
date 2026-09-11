package dashboard

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/screenstate"
	"github.com/sglavoie/agentmux/internal/session"
)

// queueTestClock is the moment every elapsed assertion below is measured
// against. A fixed clock rather than time.Now() is the whole point of passing
// `now` in: the queue is a pure function of its input, and a test that read the
// wall clock could only assert shapes.
var queueTestClock = time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

// queueGroup classifies a row the way BuildQueue does, using the real StateGroup
// rather than a stub. A test that invented its own buckets would stop proving
// anything the moment StateGroup's rules moved — which they have, once, when the
// unseen stop was promoted.
func queueGroup(s session.RunningSession) int {
	return StateGroup(s, nil, time.Minute)
}

// statusVocabulary is the whole status column said out loud, one entry per state
// the queue can be asked about. It is a package-level table rather than a literal
// inside one test because two tests read it: the one that checks each sentence,
// and the one that checks the set of states it covers against the footer's.
var statusVocabulary = []struct {
	name string
	s    session.RunningSession
	want string
}{
	{"terminal failure", session.RunningSession{State: "error"}, statusFailed},
	{"blocked on a permission prompt", session.RunningSession{State: "key"}, statusNeedsPermission},
	{"elicitation", session.RunningSession{State: "question"}, statusAwaitingReply},
	{"unread completion", session.RunningSession{State: "stop", Unseen: true}, statusDone},
	{"seen completion", session.RunningSession{State: "stop"}, statusDone},
	{"recovered tool failure", session.RunningSession{State: "warning"}, statusToolFailed},
	{"working", session.RunningSession{State: "bolt"}, statusWorking},
	{"subagent", session.RunningSession{State: "subagent"}, statusSubagent},
	{"compacting", session.RunningSession{State: "compact"}, statusCompacting},
	{"iconless and fresh", session.RunningSession{WindowID: 7}, statusActive},
}

// TestStatusTextSaysWhatTheRowIsDoing walks the whole state vocabulary. The
// queue's status column replaced a glyph with a sentence, so the table is the
// vocabulary: an entry that disappeared or changed wording is a column that
// silently started saying something else.
func TestStatusTextSaysWhatTheRowIsDoing(t *testing.T) {
	for _, tc := range statusVocabulary {
		t.Run(tc.name, func(t *testing.T) {
			if got := StatusText(tc.s, queueGroup(tc.s)); got != tc.want {
				t.Errorf("StatusText = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestStatusVocabularyCoversTheSameStatesAsTheFooterGloss keeps the package's two
// readable state vocabularies from drifting apart. They are deliberately separate
// — StateGloss explains an ICON ("stopped"), the status column describes the
// SESSION ("Done"), and only the latter is refined by a prompt kind or aware of
// which bucket an iconless row fell into — but a state that gained one and not
// the other would show a footer explaining something the row never said, or a row
// saying something the legend cannot explain.
//
// The one asymmetry is the iconless row: its gloss is the absence of an icon, so
// it is in the status vocabulary and not in the gloss map, by design.
func TestStatusVocabularyCoversTheSameStatesAsTheFooterGloss(t *testing.T) {
	covered := make(map[string]bool, len(statusVocabulary))
	for _, tc := range statusVocabulary {
		covered[tc.s.State] = true
		if tc.want == "" {
			t.Errorf("state %q is in the vocabulary with no sentence", tc.s.State)
		}
	}

	for state := range stateGlosses {
		if !covered[state] {
			t.Errorf("state %q has a footer gloss but no status sentence in the queue vocabulary", state)
		}
		if got := StatusText(session.RunningSession{State: state}, activeGroup); got == "" {
			t.Errorf("state %q has a footer gloss but StatusText says nothing about it", state)
		}
	}
	for state := range covered {
		if state == "" {
			continue
		}
		if stateGlosses[state] == "" {
			t.Errorf("state %q has a status sentence but no footer gloss; the legend cannot explain it", state)
		}
	}
	if !covered[""] {
		t.Error("the iconless state is missing from the status vocabulary")
	}
}

// TestIconlessRowReadsItsBucket pins the one status that is not a property of
// the row at all. Whether a session with no icon is Active or Idle is a judgement
// about how long the daemon has been silent, which StateGroup already made — and
// the group is passed in rather than recomputed so the sentence and the heading
// the row is drawn under cannot disagree about the same session.
func TestIconlessRowReadsItsBucket(t *testing.T) {
	s := session.RunningSession{WindowID: 7}
	stale := map[int]time.Time{7: queueTestClock.Add(-time.Hour)}

	if got := StatusText(s, StateGroup(s, nil, time.Minute)); got != statusActive {
		t.Errorf("a row with no activity record reads %q, want %q", got, statusActive)
	}
	if got := StatusText(s, StateGroup(s, stale, time.Minute)); got != statusIdle {
		t.Errorf("a row silent past the threshold reads %q, want %q", got, statusIdle)
	}
}

// TestReasonStatusesCoverEveryPromptKind is the totality guard the untyped wire
// field cannot give us, and the sentence-length twin of
// TestReasonTagsCoverEveryPromptKind. state.WindowState.PromptKind is a bare
// string so Claude and Codex kinds can share it without a shared enum, which
// means nothing in the type system notices when a tenth kind is added and the
// queue silently stops refining it.
func TestReasonStatusesCoverEveryPromptKind(t *testing.T) {
	kinds := []string{
		string(screenstate.CodexPromptEditApproval),
		string(screenstate.CodexPromptRequestUserInput),
		string(screenstate.CodexPromptUnansweredConfirmation),
		string(screenstate.CodexPromptCommandApproval),
		string(screenstate.CodexPromptPermissionGrant),
		string(screenstate.ClaudePromptPermission),
		string(screenstate.ClaudePromptTrust),
		string(screenstate.ClaudePromptEdit),
		string(screenstate.ClaudePromptElicitation),
	}
	for _, kind := range kinds {
		if reasonStatuses[kind] == "" {
			t.Errorf("prompt kind %q has no status sentence; decide what it reads as in reasonStatuses", kind)
		}
		if reasonTags[kind] == "" {
			t.Errorf("prompt kind %q has a status sentence but no column tag; the two vocabularies must cover the same kinds", kind)
		}
	}
	if len(reasonStatuses) != len(kinds) {
		t.Errorf("reasonStatuses has %d entries for %d known kinds: an entry here that no harness can stamp is dead presentation",
			len(reasonStatuses), len(kinds))
	}
}

// TestBlockedStatusIsRefinedByItsPromptKind is the load-bearing fusion: the state
// says the row is waiting and the prompt kind says what for, and they are one
// sentence rather than two columns because `Needs permission` and `Needs edit
// approval` are the same fact at two levels of detail.
func TestBlockedStatusIsRefinedByItsPromptKind(t *testing.T) {
	cases := []struct {
		state string
		kind  string
		want  string
	}{
		{"key", string(screenstate.ClaudePromptPermission), statusNeedsPermission},
		{"key", string(screenstate.CodexPromptCommandApproval), statusNeedsPermission},
		{"key", string(screenstate.CodexPromptPermissionGrant), statusNeedsPermission},
		{"key", string(screenstate.ClaudePromptEdit), statusNeedsEdit},
		{"key", string(screenstate.CodexPromptEditApproval), statusNeedsEdit},
		{"key", string(screenstate.ClaudePromptTrust), statusNeedsTrust},
		{"key", string(screenstate.CodexPromptRequestUserInput), statusNeedsInput},
		{"question", string(screenstate.ClaudePromptElicitation), statusAwaitingReply},
		{"question", string(screenstate.CodexPromptUnansweredConfirmation), statusAwaitingReply},
		// Both harnesses' permission prompts read alike, which is the point: the
		// operator's answer to all three wire kinds is the same.
		{"question", string(screenstate.ClaudePromptPermission), statusNeedsPermission},
	}
	for _, tc := range cases {
		s := session.RunningSession{State: tc.state, Reason: tc.kind}
		if got := StatusText(s, queueGroup(s)); got != tc.want {
			t.Errorf("StatusText(state=%q, kind=%q) = %q, want %q", tc.state, tc.kind, got, tc.want)
		}
	}
}

// TestUnlabelledBlockedRowKeepsItsDefaultSentence covers the COMMON case, not a
// failure: Copilot has no screen adapter, a hook-raised 🔑 arrives before any
// probe reads the pane, and a pattern override may have dropped the clause that
// names the kind. The icon is evidence enough for the sentence even when nothing
// refined it.
func TestUnlabelledBlockedRowKeepsItsDefaultSentence(t *testing.T) {
	blocked := session.RunningSession{State: "key"}
	if got := StatusText(blocked, queueGroup(blocked)); got != statusNeedsPermission {
		t.Errorf("an unlabelled blocked row reads %q, want %q", got, statusNeedsPermission)
	}
	asking := session.RunningSession{State: "question"}
	if got := StatusText(asking, queueGroup(asking)); got != statusAwaitingReply {
		t.Errorf("an unlabelled question row reads %q, want %q", got, statusAwaitingReply)
	}
}

// TestUnknownPromptKindIsNotRenderedRaw pins the skew case: a daemon newer than
// the dashboard reading it. A bare `unanswered_confirmation` in the status column
// is noise, and the state's own sentence is still true.
func TestUnknownPromptKindIsNotRenderedRaw(t *testing.T) {
	s := session.RunningSession{State: "key", Reason: "some_future_kind"}
	got := StatusText(s, queueGroup(s))
	if got == "some_future_kind" {
		t.Fatal("the raw wire kind reached the status column")
	}
	if got != statusNeedsPermission {
		t.Errorf("StatusText = %q, want the state's own sentence %q", got, statusNeedsPermission)
	}
}

// TestUnknownStateSaysNothing is the other half of the skew rule, and the one
// place saying nothing beats saying something. Falling back to the bucket an
// unrecognized state was sorted into would print `Idle` over a session that might
// be anything at all.
func TestUnknownStateSaysNothing(t *testing.T) {
	s := session.RunningSession{State: "hologram"}
	if got := StatusText(s, queueGroup(s)); got != "" {
		t.Errorf("StatusText(unknown state) = %q, want \"\"", got)
	}
	if got := StatusEmphasis(s, queueGroup(s)); got != EmphasisMuted {
		t.Errorf("StatusEmphasis(unknown state) = %v, want muted: it sorted into Idle", got)
	}
}

// TestStatusEmphasisIsSemanticNotPerState pins the ordering the palette rules
// rest on: everything in Needs you is amber, so an unread completion and a
// permission prompt look alike — they are alike, both are the operator's turn —
// and only a terminal failure escalates past them.
func TestStatusEmphasisIsSemanticNotPerState(t *testing.T) {
	cases := []struct {
		name string
		s    session.RunningSession
		want Emphasis
	}{
		{"terminal failure", session.RunningSession{State: "error"}, EmphasisFailure},
		{"permission prompt", session.RunningSession{State: "key"}, EmphasisAttention},
		{"elicitation", session.RunningSession{State: "question"}, EmphasisAttention},
		{"unread completion", session.RunningSession{State: "stop", Unseen: true}, EmphasisAttention},
		{"seen completion", session.RunningSession{State: "stop"}, EmphasisMuted},
		{"recovered tool failure", session.RunningSession{State: "warning"}, EmphasisNeutral},
		{"working", session.RunningSession{State: "bolt"}, EmphasisNeutral},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := StatusEmphasis(tc.s, queueGroup(tc.s)); got != tc.want {
				t.Errorf("StatusEmphasis = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestRecoveredToolFailureDoesNotWearTheAttentionAccent states the warning rule
// on its own, because it is the one the redesign changes. A warning sits in
// Active — the harness recovered and kept going — so painting it amber in the
// queue would put a row that needs nothing beside the rows that do.
func TestRecoveredToolFailureDoesNotWearTheAttentionAccent(t *testing.T) {
	warned := session.RunningSession{State: "warning"}
	if got := queueGroup(warned); got != activeGroup {
		t.Fatalf("the fixture is in group %d, want Active: the premise of this test is gone", got)
	}
	if got := StatusEmphasis(warned, queueGroup(warned)); got == EmphasisAttention || got == EmphasisFailure {
		t.Errorf("StatusEmphasis(warning) = %v; a recovered tool failure must not read as the operator's turn", got)
	}
}

// TestElapsedReadsTheClockItsBucketMeans is the anchor rule. A running row is
// measured from BusySince ("how long has this been working"); a waiting or
// resting row from PresentationChangedAt ("how long has this been waiting for
// me"). They are different questions about different moments.
func TestElapsedReadsTheClockItsBucketMeans(t *testing.T) {
	busy := queueTestClock.Add(-90 * time.Second)
	changed := queueTestClock.Add(-30 * time.Minute)

	running := session.RunningSession{State: "bolt", BusySince: busy, PresentationChangedAt: changed}
	if got, want := ElapsedText(running, queueGroup(running), queueTestClock), "1:30"; got != want {
		t.Errorf("a running row reads %q, want %q from BusySince", got, want)
	}

	waiting := session.RunningSession{State: "key", BusySince: busy, PresentationChangedAt: changed}
	if got, want := ElapsedText(waiting, queueGroup(waiting), queueTestClock), "30:00"; got != want {
		t.Errorf("a waiting row reads %q, want %q from PresentationChangedAt", got, want)
	}

	resting := session.RunningSession{State: "stop", BusySince: busy, PresentationChangedAt: changed}
	if got, want := ElapsedText(resting, queueGroup(resting), queueTestClock), "30:00"; got != want {
		t.Errorf("a resting row reads %q, want %q from PresentationChangedAt", got, want)
	}
}

// TestUnknownClockSaysNothingRatherThanSubstituting is the no-invented-timestamps
// rule, and the substitution is the trap: a running row whose BusySince is
// missing would report the age of its last icon change as its run time — a number
// that looks exactly like the truth and is not it.
func TestUnknownClockSaysNothingRatherThanSubstituting(t *testing.T) {
	running := session.RunningSession{State: "bolt", PresentationChangedAt: queueTestClock.Add(-30 * time.Minute)}
	if got := ElapsedText(running, queueGroup(running), queueTestClock); got != "—" {
		t.Errorf("a running row with no BusySince reads %q, want \"—\"", got)
	}
	waiting := session.RunningSession{State: "key", BusySince: queueTestClock.Add(-90 * time.Second)}
	if got := ElapsedText(waiting, queueGroup(waiting), queueTestClock); got != "—" {
		t.Errorf("a waiting row with no PresentationChangedAt reads %q, want \"—\"", got)
	}
	var nothing session.RunningSession
	if got := ElapsedText(nothing, queueGroup(nothing), queueTestClock); got != "—" {
		t.Errorf("a row with no clock at all reads %q, want \"—\"", got)
	}
}

// TestEqualTimestampsReadAlikeAndSkewReadsZero covers the two degenerate clocks.
// Equal anchors must produce equal text — the queue's recency ordering already
// leaves such rows in their incoming order, and a column that broke the tie
// visually would contradict it — and an anchor in the future clamps to zero so
// skew between the daemon and the TUI cannot surface a count running backwards.
func TestEqualTimestampsReadAlikeAndSkewReadsZero(t *testing.T) {
	at := queueTestClock.Add(-5 * time.Minute)
	first := session.RunningSession{State: "key", PresentationChangedAt: at}
	second := session.RunningSession{State: "error", PresentationChangedAt: at}
	if a, b := ElapsedText(first, attentionGroup, queueTestClock), ElapsedText(second, attentionGroup, queueTestClock); a != b {
		t.Errorf("equal timestamps read %q and %q", a, b)
	}

	future := session.RunningSession{State: "bolt", BusySince: queueTestClock.Add(time.Hour)}
	if got, want := ElapsedText(future, activeGroup, queueTestClock), "0:00"; got != want {
		t.Errorf("a clock-skewed anchor reads %q, want %q", got, want)
	}
}

// TestSeenAndUnseenStopsDifferOnlyInTheUnreadFact pins the division of labour the
// stop case rests on. Seenness does not change what happened to the session — the
// turn finished either way — it changes whether the operator has looked at it. So
// the sentence is the same and the bucket, the emphasis, and the Unread flag are
// what move.
func TestSeenAndUnseenStopsDifferOnlyInTheUnreadFact(t *testing.T) {
	rows := []session.RunningSession{
		{WindowID: 11, Base: "unread", State: "stop", Unseen: true},
		{WindowID: 12, Base: "seen", State: "stop"},
	}
	queue := BuildQueue(QueueInput{Rows: rows, IdleThreshold: time.Minute, Now: queueTestClock})

	if queue[0].Status != queue[1].Status {
		t.Errorf("the two stops read %q and %q; both finished their turn", queue[0].Status, queue[1].Status)
	}
	if !queue[0].Unread {
		t.Error("the unseen stop is not marked unread")
	}
	if queue[1].Unread {
		t.Error("the seen stop is marked unread")
	}
	if queue[0].Group != attentionGroup {
		t.Errorf("the unseen stop is in group %d, want Needs you", queue[0].Group)
	}
	if queue[1].Group != idleGroup {
		t.Errorf("the seen stop is in group %d, want Idle", queue[1].Group)
	}
	if queue[0].Emphasis != EmphasisAttention || queue[1].Emphasis != EmphasisMuted {
		t.Errorf("emphases are %v and %v, want attention and muted", queue[0].Emphasis, queue[1].Emphasis)
	}
}

// TestBuildQueueIsIndexPreserving pins the pairing contract: queue row i
// describes Rows[i], so a caller holding an item list can pair a line with its
// facts without a second lookup.
func TestBuildQueueIsIndexPreserving(t *testing.T) {
	rows := []session.RunningSession{
		{WindowID: 11, Project: "API", State: "key"},
		{WindowID: 12, Project: "Web", State: "bolt", BusySince: queueTestClock.Add(-time.Minute)},
		{WindowID: 13, Project: "ETL", State: "stop"},
	}
	queue := BuildQueue(QueueInput{Rows: rows, IdleThreshold: time.Minute, Now: queueTestClock})

	if len(queue) != len(rows) {
		t.Fatalf("BuildQueue produced %d rows for %d sessions", len(queue), len(rows))
	}
	for i, row := range queue {
		if row.Row != i {
			t.Errorf("queue row %d carries Row = %d", i, row.Row)
		}
		if want := SessionDisplayTitle(rows[i]); row.Name != want {
			t.Errorf("queue row %d is named %q, want %q", i, row.Name, want)
		}
	}
}

// TestBuildQueueWithoutALiveSetUsesItsRows pins the documented degraded case: a
// caller with a single unfiltered list means that list IS the live set, and the
// collisions inside it still have to be explained.
func TestBuildQueueWithoutALiveSetUsesItsRows(t *testing.T) {
	rows := collidingPair()
	queue := BuildQueue(QueueInput{Rows: rows, IdleThreshold: time.Minute, Now: queueTestClock})
	if got, want := queue[1].Qualifier, "amx/fix"; got != want {
		t.Errorf("Qualifier = %q, want %q", got, want)
	}
	if got, want := queue[1].Label(), "API (amx/fix)"; got != want {
		t.Errorf("Label = %q, want %q", got, want)
	}
	if got, want := queue[1].Name, "API"; got != want {
		t.Errorf("Name = %q, want the unqualified title %q: the halves are drawn with different emphasis", got, want)
	}
}

// TestBuildQueueIsPure is the property everything else here depends on. The same
// rows and the same clock must produce the same queue, because a presentation
// layer that read the wall clock, a map's iteration order, or anything outside
// its input could not be asserted at all.
func TestBuildQueueIsPure(t *testing.T) {
	rows := []session.RunningSession{
		{WindowID: 11, Project: "API", Directory: "/repo/api", Branch: "main", State: "key", PresentationChangedAt: queueTestClock.Add(-time.Minute)},
		{WindowID: 12, Project: "API", Directory: "/wt/fix", Branch: "amx/fix", State: "bolt", BusySince: queueTestClock.Add(-time.Hour)},
		{WindowID: 13, Project: "Web", State: "stop", Unseen: true},
		{WindowID: 14},
	}
	in := QueueInput{Rows: rows, Live: rows, IdleThreshold: time.Minute, Now: queueTestClock}
	first := BuildQueue(in)
	for attempt := 0; attempt < 25; attempt++ {
		again := BuildQueue(in)
		for i := range first {
			if first[i] != again[i] {
				t.Fatalf("attempt %d row %d: %+v != %+v", attempt, i, again[i], first[i])
			}
		}
	}
}

// TestEmphasisNamesItself keeps the diagnostic vocabulary honest — every failure
// message above prints an emphasis, and an unnamed one would print as an integer.
func TestEmphasisNamesItself(t *testing.T) {
	cases := map[Emphasis]string{
		EmphasisNeutral:   "neutral",
		EmphasisMuted:     "muted",
		EmphasisAttention: "attention",
		EmphasisFailure:   "failure",
	}
	for e, want := range cases {
		if got := e.String(); got != want {
			t.Errorf("Emphasis(%d).String() = %q, want %q", int(e), got, want)
		}
	}
}

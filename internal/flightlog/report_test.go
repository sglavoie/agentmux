package flightlog

import (
	"testing"
	"time"
)

func at(seconds int) time.Time { return time.Date(2026, 10, 26, 10, 0, seconds, 0, time.UTC) }
func event(id, key, kind, icon string, seconds int) Event {
	return Event{ID: id, Key: key, RuntimeSessionID: key, At: at(seconds), Kind: kind, Icon: icon, Harness: "codex", Project: "API", Profile: "work", Epoch: "daemon-1"}
}
func testSnapshot(events ...Event) Snapshot {
	return Snapshot{SchemaVersion: SchemaVersion, TrackingSince: at(-1), Settings: Settings{Enabled: true, Celebrations: true}, Events: events}
}

func TestReportsCountLaunchesByIdentityAndPreserveConversationContinuity(t *testing.T) {
	first := event("launch-1", "rs-one", "launch", "", 0)
	first.LaunchID = "l-one"
	first.Operation = "fresh"
	first.ConversationID = "codex:conversation"
	resume := event("launch-2", "rs-two", "launch", "", 5)
	resume.LaunchID = "l-two"
	resume.Operation = "resume"
	resume.ConversationID = first.ConversationID
	fork := event("launch-3", "rs-three", "launch", "", 10)
	fork.LaunchID = "l-three"
	fork.Operation = "fork"
	fork.ConversationID = "codex:fork"
	restart := event("launch-4", "rs-four", "launch", "", 15)
	restart.LaunchID = "l-four"
	restart.Operation = "restart"
	report := Build(testSnapshot(first, resume, first, fork, restart), Filters{Period: "all"}, at(60), []string{"codex"})
	if report.Totals.Launches != 4 || report.Totals.Fresh != 1 || report.Totals.Resumes != 1 || report.Totals.Forks != 1 || report.Totals.Restarts != 1 || report.Totals.Conversations != 2 {
		t.Fatalf("wrong counters: %+v", report.Totals)
	}
	if len(report.Sessions) != 4 {
		t.Fatalf("resuming merged sessions: %+v", report.Sessions)
	}
	if report.Achievements[0].UnlockedAt == nil || !report.Achievements[0].UnlockedAt.Equal(first.At) || report.Achievements[0].SessionKey != "rs-one" {
		t.Fatalf("missing earning session: %+v", report.Achievements[0])
	}
	filtered := Build(testSnapshot(first, resume, fork), Filters{Period: "all", Session: "rs-two"}, at(60), []string{"codex"})
	if filtered.Totals.Launches != 1 || filtered.Lifetime.Launches != 3 || len(filtered.Sessions) != 1 {
		t.Fatalf("filter leaked or changed lifetime totals: %+v", filtered)
	}
}

func TestStateChangesIgnoreNoopsAndLoopOnlyChangesDoNotFinishTurns(t *testing.T) {
	work := event("work", "rs", "state", "bolt", 0)
	question := event("question", "rs", "state", "question", 10)
	question.BeforeIcon = "bolt"
	permission := event("permission", "rs", "state", "key", 20)
	permission.BeforeIcon = "question"
	stop := event("stop", "rs", "state", "stop", 30)
	stop.BeforeIcon = "key"
	stop.Looping = true
	loop := event("loop", "rs", "state", "stop", 40)
	loop.BeforeIcon = "stop"
	loop.BeforeLooping = true
	noop := event("noop", "rs", "state", "stop", 45)
	noop.BeforeIcon = "stop"
	r := Build(testSnapshot(work, question, permission, stop, loop, noop, stop), Filters{Period: "all"}, at(60), []string{"codex"})
	m := r.Totals
	if m.StateChanges != 5 || m.TurnsFinished != 1 || m.Questions != 1 || m.Permissions != 1 || m.WorkingSeconds != 10 || m.WaitingSeconds != 20 || m.ObservedSeconds != 45 {
		t.Fatalf("wrong transition/duration accounting: %+v", m)
	}
	if r.Achievements[5].UnlockedAt == nil || !r.Achievements[5].UnlockedAt.Equal(stop.At) {
		t.Fatalf("fleet unlock: %+v", r.Achievements[5])
	}
}

func TestObservationGapsAndDaemonRestartsNeverAccrueWorkingTime(t *testing.T) {
	a := event("a", "rs", "state", "bolt", 0)
	b := event("b", "rs", "sample", "bolt", 30)
	c := event("c", "rs", "sample", "bolt", 120)
	d := event("d", "rs", "sample", "bolt", 140)
	d.Epoch = "daemon-2"
	e := event("e", "rs", "sample", "bolt", 160)
	e.Epoch = "daemon-2"
	f := event("f", "rs", "sample", "bolt", 180)
	f.Epoch = "daemon-2"
	gap := Event{ID: "gap", At: at(170), Kind: "gap"}
	r := Build(testSnapshot(a, b, c, d, e, gap, f), Filters{Period: "all"}, at(600), []string{"codex"})
	if r.Totals.WorkingSeconds != 50 || r.Totals.ObservedSeconds != 50 {
		t.Fatalf("unknown time became work: %+v", r.Totals)
	}
	unknown := 0
	for _, segment := range r.Sessions[0].Timeline {
		if !segment.Observed {
			unknown++
		}
	}
	if unknown != 3 {
		t.Fatalf("missing unknown timeline intervals: %+v", r.Sessions[0].Timeline)
	}
}

func TestCalendarPeriodsClipIntervalsAndCountConcurrentAgents(t *testing.T) {
	zone, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, zone) // DST fall-back day has 25 hours
	end := start.AddDate(0, 0, 1)
	if end.Sub(start) != 25*time.Hour {
		t.Fatal("fixture must cover DST")
	}
	var events []Event
	for _, key := range []string{"a", "b"} {
		x := event(key+"1", key, "state", "bolt", 0)
		x.At = end.Add(-15 * time.Second)
		y := event(key+"2", key, "sample", "bolt", 0)
		y.At = end.Add(15 * time.Second)
		events = append(events, x, y)
	}
	r := Build(testSnapshot(events...), Filters{Period: "today"}, end.Add(time.Minute), []string{"codex"})
	if r.Totals.WorkingSeconds != 30 || r.Totals.PeakWorking != 2 || !r.Since.Equal(end) {
		t.Fatalf("boundary overlap wrong: %+v", r.Totals)
	}
	week := periodStart("week", time.Date(2026, 11, 1, 17, 0, 0, 0, zone))
	if week.Weekday() != time.Monday || week.Hour() != 0 || week.Day() != 26 {
		t.Fatalf("not a calendar week: %s", week)
	}
	var previous Day
	for _, day := range r.Days {
		if day.Date == "2026-11-01" {
			previous = day
		}
	}
	if previous.Metrics.WorkingSeconds != 30 {
		t.Fatalf("midnight split lost duration: %+v", previous)
	}
}

func TestFleetUnlockSurvivesConfigurationChangesAndRunReplay(t *testing.T) {
	launch := event("launch", "rs", "launch", "", 0)
	launch.LaunchID = "launch"
	launch.Fleet = []string{"codex"}
	stop := event("stop", "rs", "state", "stop", 5)
	stop.BeforeIcon = "bolt"
	run := event("run", "rs", "run", "", 10)
	run.RunID = "run-one"
	run.Name = "AgentSessionLoopFinished"
	replay := run
	replay.ID = "another-delivery"
	next := event("next", "rs2", "launch", "", 20)
	next.LaunchID = "next"
	next.Fleet = []string{"codex", "claude"}
	r := Build(testSnapshot(launch, stop, run, replay, next), Filters{Period: "all"}, at(60), []string{"codex", "claude"})
	if r.Totals.RunsFinished != 1 || r.Achievements[6].Progress != 1 {
		t.Fatalf("run replay counted twice: %+v", r.Totals)
	}
	if r.Achievements[5].UnlockedAt == nil || r.Achievements[5].Target != 1 || !r.Achievements[5].UnlockedAt.Equal(stop.At) {
		t.Fatalf("configuration revoked unlock: %+v", r.Achievements[5])
	}
}

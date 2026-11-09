package flightlog

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

// Build computes calendar periods in the supplied local timezone. Achievements
// always describe the whole log; dashboard filters never revoke an unlock.
func Build(snapshot Snapshot, filters Filters, now time.Time, configuredHarnesses []string) Report {
	if filters.Period == "" {
		filters.Period = "week"
	}
	events := uniqueEvents(snapshot.Events, now)
	r := Report{SchemaVersion: SchemaVersion, TrackingSince: snapshot.TrackingSince, GeneratedAt: now, Timezone: now.Location().String(),
		Settings: snapshot.Settings, Filters: filters, Days: []Day{}, Sessions: []Session{}, Recap: []string{}, Warnings: []string{}, Projects: []string{}, Harnesses: []string{}, Profiles: []string{}}
	if r.Timezone == "Local" {
		r.Timezone = now.Format("MST (-07:00)")
	}
	r.Since = periodStart(filters.Period, now)
	all := sessions(events)
	var selected []Session
	for _, s := range all {
		r.Projects = addUnique(r.Projects, s.Project)
		r.Harnesses = addUnique(r.Harnesses, s.Harness)
		r.Profiles = addUnique(r.Profiles, s.Profile)
		if filters.Project != "" && s.Project != filters.Project || filters.Harness != "" && s.Harness != filters.Harness || filters.Profile != "" && s.Profile != filters.Profile || filters.Session != "" && s.Key != filters.Session && s.RuntimeSessionID != filters.Session && s.LaunchID != filters.Session {
			continue
		}
		selected = append(selected, s)
		view := projectSession(s, r.Since, now)
		if len(view.Events) > 0 || len(view.Timeline) > 0 {
			r.Sessions = append(r.Sessions, view)
		}
	}
	slices.Sort(r.Projects)
	slices.Sort(r.Harnesses)
	slices.Sort(r.Profiles)
	r.Totals = aggregate(selected, r.Since, now)
	r.Lifetime = aggregate(all, time.Time{}, now)
	r.Achievements = achievements(events, configuredHarnesses)
	start := midnight(now.AddDate(-1, 0, 0))
	r.Days = calendarDays(selected, start, now)
	r.Recap = recap(selected, periodStart("week", now), now)
	missionReport(&r, events, now)
	if r.TrackingSince.IsZero() {
		r.Warnings = append(r.Warnings, "Tracking has not started. Start the daemon or launch a session.")
	}
	if !snapshot.Settings.Enabled {
		r.Warnings = append(r.Warnings, "Tracking is paused. Activity during this period is not recorded.")
	}
	if snapshot.CorruptLines > 0 {
		r.Warnings = append(r.Warnings, fmt.Sprintf("%d unreadable ledger records were skipped; totals are incomplete. The original data is preserved.", snapshot.CorruptLines))
	}
	partial, gap := false, false
	for _, e := range events {
		partial = partial || e.Partial
		gap = gap || e.Kind == "gap"
	}
	if partial {
		r.Warnings = append(r.Warnings, "Some history is partial or imported. Launches, identities, and observation times may be missing.")
	}
	if gap {
		r.Warnings = append(r.Warnings, "Tracking interruptions or recorder failures left gaps; activity during those gaps is not included.")
	}
	r.Warnings = append(r.Warnings, "Time totals sum observed session time, including overlapping agents. Gaps and time after the last observation are unknown.")
	return r
}

func midnight(t time.Time) time.Time {
	y, m, d := t.Date()
	start := time.Date(y, m, d, 0, 0, 0, 0, t.Location())
	// When a clock jumps forward at midnight, Date may normalize the
	// nonexistent time into the previous evening. The zone transition is
	// the first real instant of the requested day.
	if start.Year() != y || start.Month() != m || start.Day() != d {
		_, start = start.ZoneBounds()
	}
	return start
}

func calendarDay(t time.Time, offset int) time.Time {
	y, m, d := t.Date()
	// Move between dates at noon, then resolve each day's start afresh.
	// Adding a date to midnight can stay on the old date when midnight is
	// skipped, or carry a 01:00 start into every subsequent day.
	return midnight(time.Date(y, m, d+offset, 12, 0, 0, 0, t.Location()))
}
func periodStart(period string, now time.Time) time.Time {
	switch period {
	case "today":
		return midnight(now)
	case "week":
		return calendarDay(now, -(int(now.Weekday())+6)%7)
	default:
		return time.Time{}
	}
}

func uniqueEvents(events []Event, now time.Time) []Event {
	out := slices.Clone(events)
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	seen := map[string]bool{}
	filtered := make([]Event, 0, len(out))
	for _, e := range out {
		if e.ID == "" || seen[e.ID] || e.At.After(now) {
			continue
		}
		seen[e.ID] = true
		filtered = append(filtered, e)
	}
	return filtered
}

func sessions(events []Event) []Session {
	var gaps []time.Time
	for _, event := range events {
		if event.Kind == "gap" {
			gaps = append(gaps, event.At)
		}
	}
	// Launch metadata can arrive after an early hook. Resolve aliases in a
	// separate pass, without merging two launches of the same conversation.
	aliases := map[string]string{}
	for _, e := range events {
		if e.RuntimeSessionID != "" && e.LaunchID != "" {
			aliases["launch:"+e.LaunchID] = e.RuntimeSessionID
		}
	}
	byKey := map[string]*Session{}
	for _, e := range events {
		key := e.Key
		if e.RuntimeSessionID != "" {
			key = e.RuntimeSessionID
		} else if e.LaunchID != "" {
			key = "launch:" + e.LaunchID
		}
		if a := aliases[key]; a != "" {
			key = a
		}
		if key == "" {
			continue
		}
		e.Key = key
		s := byKey[key]
		if s == nil {
			firstAt := e.At
			if e.From != nil && e.From.Before(firstAt) {
				firstAt = *e.From
			}
			s = &Session{Key: key, FirstAt: firstAt, Events: []Event{}, Timeline: []Segment{}, StateCounts: map[string]int{}}
			byKey[key] = s
		}
		if e.From != nil && e.From.Before(s.FirstAt) {
			s.FirstAt = *e.From
		}
		if e.RuntimeSessionID != "" {
			s.RuntimeSessionID = e.RuntimeSessionID
		}
		if e.LaunchID != "" {
			s.LaunchID = e.LaunchID
		}
		if e.ConversationID != "" && e.Kind != "span" {
			s.ConversationID = e.ConversationID
		}
		if e.Harness != "" {
			s.Harness = e.Harness
		}
		if e.Profile != "" {
			s.Profile = e.Profile
		}
		if e.Project != "" {
			s.Project = e.Project
		}
		if e.Operation != "" {
			s.Operation = e.Operation
		}
		s.LastAt = e.At
		s.Partial = s.Partial || e.Partial
		s.Events = append(s.Events, e)
	}
	out := make([]Session, 0, len(byKey))
	for _, s := range byKey {
		// Each pair of fresh observations brackets an interval. Intervening
		// transitions split it; an epoch change or missed sample makes it unknown.
		var previous *Event
		for i := range s.Events {
			e := &s.Events[i]
			if e.Kind == "span" && e.From != nil && e.At.After(*e.From) {
				s.Timeline = append(s.Timeline, Segment{From: *e.From, To: e.At, Icon: e.Icon, Looping: e.Looping, Observed: e.Observed, ConversationID: e.ConversationID})
			}
		}
		cutoff := time.Time{}
		for _, segment := range s.Timeline {
			cutoff = maxTime(cutoff, segment.To)
		}
		for i := range s.Events {
			e := &s.Events[i]
			if e.At.Before(cutoff) {
				continue
			}
			if e.Kind != "sample" && e.Kind != "state" && e.Kind != "exit" {
				continue
			}
			if previous != nil && e.At.After(previous.At) {
				observed := e.Epoch != "" && e.Epoch == previous.Epoch && e.At.Sub(previous.At) <= ObservationLimit && !e.Partial && !previous.Partial
				if observed {
					i := sort.Search(len(gaps), func(i int) bool { return !gaps[i].Before(previous.At) })
					if i < len(gaps) && !gaps[i].After(e.At) {
						observed = false
					}
				}
				s.Timeline = append(s.Timeline, Segment{From: previous.At, To: e.At, Icon: previous.Icon, Looping: previous.Looping, Observed: observed, ConversationID: previous.ConversationID})
			}
			previous = e
			if e.Kind == "exit" {
				previous = nil
			}
		}
		sort.SliceStable(s.Timeline, func(i, j int) bool { return s.Timeline[i].From.Before(s.Timeline[j].From) })
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].LastAt.Equal(out[j].LastAt) {
			return out[i].Key < out[j].Key
		}
		return out[i].LastAt.After(out[j].LastAt)
	})
	return out
}

func projectSession(s Session, since, until time.Time) Session {
	view := s
	view.Events = []Event{}
	view.Timeline = []Segment{}
	view.StateCounts = map[string]int{}
	for _, e := range s.Events {
		if e.At.Before(since) || !e.At.Before(until) {
			continue
		}
		if e.Kind != "sample" && e.Kind != "span" {
			view.Events = append(view.Events, e)
		}
		if changed(e) {
			view.StateCounts[iconName(e.Icon)]++
		}
	}
	for _, segment := range s.Timeline {
		segment.From = maxTime(segment.From, since)
		segment.To = minTime(segment.To, until)
		if segment.To.After(segment.From) {
			view.Timeline = append(view.Timeline, segment)
		}
	}
	view.Metrics = aggregate([]Session{s}, since, until)
	return view
}

func changed(e Event) bool {
	return e.Kind == "state" && (e.BeforeIcon != e.Icon || e.BeforeLooping != e.Looping)
}
func aggregate(sessions []Session, since, until time.Time) Metrics {
	m := Metrics{}
	conversations := map[string]bool{}
	projects := map[string]bool{}
	launches := map[string]bool{}
	runs := map[string]bool{}
	type edge struct {
		at    time.Time
		delta int
	}
	var edges []edge
	for _, s := range sessions {
		active := false
		for _, e := range s.Events {
			if e.At.Before(since) || !e.At.Before(until) || e.Kind == "span" {
				continue
			}
			active = true
			if e.ConversationID != "" {
				conversations[e.ConversationID] = true
			}
			if e.Kind == "sample" {
				continue
			}
			if e.Kind == "launch" && !launches[e.LaunchID] {
				launches[e.LaunchID] = true
				m.Launches++
				switch e.Operation {
				case "resume":
					m.Resumes++
				case "fork":
					m.Forks++
				case "restart":
					m.Restarts++
				default:
					m.Fresh++
				}
			}
			if changed(e) {
				m.StateChanges++
				if e.BeforeIcon != e.Icon {
					switch e.Icon {
					case "stop":
						m.TurnsFinished++
					case "key":
						m.Permissions++
					case "question":
						m.Questions++
					case "error":
						m.Errors++
					}
				}
			}
			if e.Kind == "run" && e.Name == "AgentSessionLoopFinished" && e.RunID != "" && !runs[e.RunID] {
				runs[e.RunID] = true
				m.RunsFinished++
			}
		}
		for _, segment := range s.Timeline {
			from, to := maxTime(segment.From, since), minTime(segment.To, until)
			if !segment.Observed || !to.After(from) {
				continue
			}
			active = true
			if segment.ConversationID != "" {
				conversations[segment.ConversationID] = true
			}
			d := to.Sub(from).Seconds()
			m.ObservedSeconds += d
			if working(segment.Icon) {
				m.WorkingSeconds += d
				edges = append(edges, edge{from, 1}, edge{to, -1})
			}
			if waiting(segment.Icon) {
				m.WaitingSeconds += d
			}
		}
		if active {
			if s.Project != "" {
				projects[s.Project] = true
			}
		}
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].at.Equal(edges[j].at) {
			return edges[i].delta < edges[j].delta
		}
		return edges[i].at.Before(edges[j].at)
	})
	current := 0
	for _, edge := range edges {
		current += edge.delta
		m.PeakWorking = max(m.PeakWorking, current)
	}
	m.Conversations = len(conversations)
	m.Projects = len(projects)
	return m
}

func achievements(events []Event, configured []string) []Achievement {
	defs := []Achievement{
		{ID: "first-flight", Title: "First Flight", Description: "Launch your first session", Target: 1},
		{ID: "frequent-flyer", Title: "Frequent Flyer", Description: "Reach 100 successful launches", Target: 100},
		{ID: "thousand-signals", Title: "A Thousand Signals", Description: "Observe 1,000 state changes", Target: 1000},
		{ID: "picking-up", Title: "Picking Up the Thread", Description: "Resume 25 conversations", Target: 25},
		{ID: "branching-out", Title: "Branching Out", Description: "Fork your first conversation", Target: 1},
		{ID: "across-fleet", Title: "Across the Fleet", Description: "Finish a turn with each configured harness", Target: max(1, len(uniqueStrings(configured)))},
		{ID: "loop-closed", Title: "Loop Closed", Description: "Finish your first ASL run", Target: 1},
	}
	launches := map[string]bool{}
	runs := map[string]bool{}
	harnesses := map[string]bool{}
	for _, e := range events {
		if len(e.Fleet) > 0 && defs[5].UnlockedAt == nil {
			configured = uniqueStrings(e.Fleet)
			defs[5].Target = max(1, len(configured))
		}
		launched := e.Kind == "launch" && !launches[e.LaunchID]
		if launched {
			launches[e.LaunchID] = true
		}
		finished := e.Kind == "run" && e.Name == "AgentSessionLoopFinished" && e.RunID != "" && !runs[e.RunID]
		if finished {
			runs[e.RunID] = true
		}
		if changed(e) && e.Icon == "stop" && e.BeforeIcon != e.Icon {
			harnesses[e.Harness] = true
		}
		for i := range defs {
			a := &defs[i]
			switch a.ID {
			case "first-flight", "frequent-flyer":
				if launched {
					a.Progress++
				}
			case "thousand-signals":
				if changed(e) {
					a.Progress++
				}
			case "picking-up":
				if launched && e.Operation == "resume" {
					a.Progress++
				}
			case "branching-out":
				if launched && e.Operation == "fork" {
					a.Progress++
				}
			case "across-fleet":
				if a.UnlockedAt == nil {
					a.Progress = 0
					for _, name := range configured {
						if harnesses[name] {
							a.Progress++
						}
					}
				}
			case "loop-closed":
				if finished {
					a.Progress++
				}
			}
			if a.UnlockedAt == nil && a.Progress >= a.Target {
				at := e.At
				a.UnlockedAt = &at
				a.SessionKey = e.Key
				if e.RuntimeSessionID != "" {
					a.SessionKey = e.RuntimeSessionID
				} else if e.LaunchID != "" {
					a.SessionKey = "launch:" + e.LaunchID
				}
			}
		}
	}
	return defs
}

func recap(sessions []Session, since, now time.Time) []string {
	m := aggregate(sessions, since, now)
	out := []string{fmt.Sprintf("This week: %d launches, %d resumed conversations, and %d turns finished.", m.Launches, m.Resumes, m.TurnsFinished)}
	counts := map[string]int{}
	for _, s := range sessions {
		for _, e := range s.Events {
			if !e.At.Before(since) && e.At.Before(now) && changed(e) && e.Icon == "key" && s.Project != "" {
				counts[s.Project]++
			}
		}
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	top := ""
	for _, k := range keys {
		if top == "" || counts[k] > counts[top] {
			top = k
		}
	}
	if top != "" {
		out = append(out, fmt.Sprintf("%s had the most permission requests (%d).", top, counts[top]))
	}
	if m.PeakWorking > 0 {
		out = append(out, fmt.Sprintf("Your busiest observed overlap was %d working sessions.", m.PeakWorking))
	}
	if m.RunsFinished > 0 {
		out = append(out, fmt.Sprintf("%d ASL runs finished this week.", m.RunsFinished))
	}
	return out
}

func uniqueStrings(values []string) []string {
	out := []string{}
	for _, v := range values {
		out = addUnique(out, v)
	}
	return out
}
func addUnique(values []string, value string) []string {
	if value != "" && !slices.Contains(values, value) {
		return append(values, value)
	}
	return values
}
func maxTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return b
	}
	return a
}
func minTime(a, b time.Time) time.Time {
	if a.After(b) {
		return b
	}
	return a
}
func iconName(icon string) string {
	if icon == "" {
		return "idle"
	}
	return icon
}

// Label is shared by terminal scorecards and exports.
func Label(icon string) string {
	switch icon {
	case "", "idle":
		return "Idle"
	case "bolt":
		return "Working"
	case "subagent":
		return "Subagent working"
	case "compact":
		return "Compacting context"
	case "stop":
		return "Turn finished"
	case "key":
		return "Permission requested"
	case "question":
		return "Question"
	case "error":
		return "Error"
	case "warning":
		return "Warning"
	default:
		return strings.ReplaceAll(icon, "_", " ")
	}
}

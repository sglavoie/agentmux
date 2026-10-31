// Package flightview paints Flight Log from an explicit report and selection.
// It reads no files and has no root-model or terminal-backend dependencies.
package flightview

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/sglavoie/agentmux/internal/flightlog"
)

type Input struct {
	Report              *flightlog.Report
	Loading             bool
	Error               string
	Tab, Cursor, Offset int
	Detail              bool
	EventKind           string
}

func Lines(in Input, width int) []string {
	var b strings.Builder
	b.WriteString("FLIGHT LOG     1 Overview   2 Sessions   3 Milestones\n")
	if in.Loading {
		b.WriteString("Refreshing…\n")
	}
	if in.Error != "" {
		fmt.Fprintf(&b, "Could not read Flight Log: %s\n", in.Error)
	}
	if in.Report == nil {
		if !in.Loading {
			b.WriteString("No recorded history yet. Launch a session to begin.\n")
		}
		return wrap(b.String(), width)
	}
	r := in.Report
	fmt.Fprintf(&b, "%s · project %s · harness %s · profile %s\n", r.Filters.Period, filterLabel(r.Filters.Project), filterLabel(r.Filters.Harness), filterLabel(r.Filters.Profile))
	if !r.TrackingSince.IsZero() {
		fmt.Fprintf(&b, "Tracking since %s\n", r.TrackingSince.Local().Format(time.DateTime))
	}
	if !r.Settings.Enabled {
		b.WriteString("Tracking paused\n")
	}
	b.WriteByte('\n')
	switch in.Tab {
	case 1:
		if in.Detail && in.Cursor >= 0 && in.Cursor < len(r.Sessions) {
			scorecard(&b, r.Sessions[in.Cursor], in.EventKind)
		} else {
			if len(r.Sessions) == 0 {
				b.WriteString("No sessions match these filters.\n")
			}
			for i, s := range r.Sessions {
				marker := "  "
				if i == in.Cursor {
					marker = "› "
				}
				fmt.Fprintf(&b, "%s%s · %s · %s · %d changes · %d turns\n  %s\n", marker, filterLabel(s.Project), s.Harness, s.Operation, s.Metrics.StateChanges, s.Metrics.TurnsFinished, s.Key)
			}
		}
	case 2:
		b.WriteString("MILESTONES · all time\n\n")
		for _, a := range r.Achievements {
			status := fmt.Sprintf("%d / %d", a.Progress, a.Target)
			if a.UnlockedAt != nil {
				status = "Unlocked " + a.UnlockedAt.Local().Format(time.DateOnly)
			}
			fmt.Fprintf(&b, "%s · %s\n  %s\n", a.Title, status, a.Description)
			if a.SessionKey != "" {
				fmt.Fprintf(&b, "  Earned in %s\n", a.SessionKey)
			}
			b.WriteByte('\n')
		}
	default:
		m := r.Totals
		fmt.Fprintf(&b, "%d launches · %d state changes · %d turns finished\n%d projects · %d conversations · %d ASL runs finished\n", m.Launches, m.StateChanges, m.TurnsFinished, m.Projects, m.Conversations, m.RunsFinished)
		fmt.Fprintf(&b, "Fresh %d · Resumed %d · Forked %d · Restarted %d\nObserved work %s · Waiting for you %s\nQuestions %d · Permissions %d · Peak working %d\n", m.Fresh, m.Resumes, m.Forks, m.Restarts, duration(m.WorkingSeconds), duration(m.WaitingSeconds), m.Questions, m.Permissions, m.PeakWorking)
		b.WriteString("\nACTIVITY · last 12 weeks · launches\n")
		days := r.Days[max(0, len(r.Days)-84):]
		for start := 0; start < len(days); start += 7 {
			end := min(start+7, len(days))
			fmt.Fprintf(&b, "%s  ", days[start].Date)
			for _, d := range days[start:end] {
				b.WriteString([]string{"·", "▂", "▄", "▆", "█"}[min(4, d.Metrics.Launches)])
			}
			b.WriteByte('\n')
		}
		b.WriteString("\nTHIS WEEK\n")
		for _, line := range r.Recap {
			fmt.Fprintln(&b, line)
		}
		b.WriteString("\n")
		for _, warning := range r.Warnings {
			fmt.Fprintln(&b, warning)
		}
	}
	return wrap(b.String(), width)
}

func Render(in Input, width, height int) string {
	lines := Lines(in, width)
	offset := min(max(0, in.Offset), max(0, len(lines)-height))
	if in.Tab == 1 && !in.Detail && height > 0 {
		for i, line := range lines {
			if strings.HasPrefix(line, "› ") {
				if i < offset {
					offset = i
				}
				if i+1 >= offset+height {
					offset = max(0, i+2-height)
				}
				break
			}
		}
	}
	return strings.Join(lines[offset:min(len(lines), offset+max(0, height))], "\n")
}

func scorecard(b *strings.Builder, s flightlog.Session, kind string) {
	fmt.Fprintf(b, "%s · %s · %s\n%s\n\n%d state changes · %d turns finished\nObserved work %s · Waiting %s\n%d questions · %d permissions · %d errors\n", s.Project, s.Harness, s.Operation, s.Key, s.Metrics.StateChanges, s.Metrics.TurnsFinished, duration(s.Metrics.WorkingSeconds), duration(s.Metrics.WaitingSeconds), s.Metrics.Questions, s.Metrics.Permissions, s.Metrics.Errors)
	if s.Partial {
		b.WriteString("Partial history: some identities or observations are unavailable.\n")
	}
	b.WriteString("\nTIMELINE · gaps and time after the last observation are unknown\n")
	for _, segment := range s.Timeline {
		label := flightlog.Label(segment.Icon)
		if !segment.Observed {
			label = "Unknown"
		}
		fmt.Fprintf(b, "%s  %s  %s\n", segment.From.Local().Format(time.TimeOnly), label, duration(segment.To.Sub(segment.From).Seconds()))
	}
	b.WriteString("\nSTATE BREAKDOWN\n")
	keys := make([]string, 0, len(s.StateCounts))
	for k := range s.StateCounts {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		fmt.Fprintf(b, "%s: %d\n", flightlog.Label(k), s.StateCounts[k])
	}
	fmt.Fprintf(b, "\nEVENTS · %s\n", kind)
	for _, e := range s.Events {
		if kind != "all" && (e.Kind != "state" || e.Icon != kind) {
			continue
		}
		label := e.Name
		if e.Kind == "state" {
			label = flightlog.Label(e.BeforeIcon) + " → " + flightlog.Label(e.Icon)
			if e.Looping != e.BeforeLooping {
				label += fmt.Sprintf(" · loop %t → %t", e.BeforeLooping, e.Looping)
			}
		}
		if e.Kind == "launch" {
			label = "Session launched"
		}
		fmt.Fprintf(b, "%s  %s\n", e.At.Local().Format(time.DateTime), label)
	}
}
func wrap(text string, width int) []string {
	return strings.Split(strings.TrimSuffix(ansi.Hardwrap(text, max(1, width), true), "\n"), "\n")
}
func filterLabel(value string) string {
	if value == "" {
		return "all"
	}
	return value
}
func duration(seconds float64) string { return (time.Duration(seconds) * time.Second).String() }

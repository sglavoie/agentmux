package dashboard

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
)

// SessionLocation names where a running session lives, for the surfaces that
// name one session back to the operator. A Kitty session is its window. A
// session on another backend is its backend and runtime session: the window
// ID this process lists it under is a process-local stand-in and means
// nothing to the operator.
func SessionLocation(sess session.RunningSession) string {
	kind := sess.BackendKind()
	if kind == runtimesession.BackendKitty {
		return fmt.Sprintf("window %d", sess.WindowID)
	}
	if sess.RuntimeSessionID != "" {
		return fmt.Sprintf("%s session %s", kind, sess.RuntimeSessionID)
	}
	return string(kind) + " session"
}

// SessionStateLabel is a session's state in the words its dashboard row uses
// ("Needs permission", "Working", "Idle"), grouped the way the row is.
// A state this build does not know reads as its raw name rather than nothing.
func SessionStateLabel(sess session.RunningSession, lastActivity map[int]time.Time, threshold time.Duration) string {
	if label := StatusText(sess, StateGroup(sess, lastActivity, threshold)); label != "" {
		return label
	}
	return sess.State
}

// SessionDisplayTitle is the name a running session shows under in every
// dashboard-derived surface: the row title column, the filter's substring
// match, the detail and preview headers, and the action confirmations that
// name a session back to the user. It prefers the current config title,
// then the launch project title, and finally its Kitty base name.
func SessionDisplayTitle(sess session.RunningSession) string {
	if sess.DisplayProject != "" {
		return sess.DisplayProject
	}
	if title := sess.EffectiveProject(); title != "" {
		return title
	}
	return sess.Base
}

// ProjectLabel is the one project identity used by filtering, completion, and
// the project switcher. FamilyLabel names a discovered repository family across
// configured projects and profiles; SessionDisplayTitle is the compatibility
// fallback for sessions without family metadata.
func ProjectLabel(sess session.RunningSession) string {
	if sess.FamilyLabel != "" {
		return sess.FamilyLabel
	}
	return SessionDisplayTitle(sess)
}

// CanonicalProjectLabels returns one case-insensitive aggregate per non-empty
// live project label. The highest-priority member supplies each aggregate's
// display casing, and aggregates are ordered by those same lead members.
func CanonicalProjectLabels(rows []session.RunningSession, lastActivity map[int]time.Time, threshold time.Duration) []string {
	type aggregate struct {
		label string
		lead  session.RunningSession
	}
	groups := make([]aggregate, 0, len(rows))
	for _, row := range rows {
		label := ProjectLabel(row)
		if label == "" {
			continue
		}
		found := -1
		for i := range groups {
			if strings.EqualFold(groups[i].label, label) {
				found = i
				break
			}
		}
		if found < 0 {
			groups = append(groups, aggregate{label: label, lead: row})
			continue
		}
		if compareRowPriority(row, groups[found].lead, lastActivity, threshold) < 0 {
			groups[found] = aggregate{label: label, lead: row}
		}
	}

	sort.SliceStable(groups, func(i, j int) bool {
		if cmp := compareRowPriority(groups[i].lead, groups[j].lead, lastActivity, threshold); cmp != 0 {
			return cmp < 0
		}
		li := strings.ToLower(groups[i].label)
		lj := strings.ToLower(groups[j].label)
		if li != lj {
			return li < lj
		}
		return groups[i].label < groups[j].label
	})

	labels := make([]string, len(groups))
	for i := range groups {
		labels[i] = groups[i].label
	}
	return labels
}

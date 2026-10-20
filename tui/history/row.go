package history

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/tui/layout"
)

// FormatRow returns a single-line summary of one search result.
// Format: "  <when>  <project>  [profile/harness]  matches?  preview"
//
// It is exported for the root's cross-domain styled-truncation regression,
// which asserts a session row and a plan row truncate identically; that
// comparison spans two leaves, so it stays in root.
func FormatRow(res transcript.Result, acctColor map[string]string, maxWidth int) string {
	return formatRow(res, acctColor, maxWidth, rowFilters{})
}

// rowFilters identifies labels already supplied by the active filter chips.
// Keep differing labels (including shared-profile qualifiers) informative.
type rowFilters struct {
	project string
	profile string
	harness string
}

func formatRow(res transcript.Result, acctColor map[string]string, maxWidth int, filters rowFilters) string {
	when := layout.When(res.Meta.LastAt, time.Now())
	project := res.Meta.Project
	if project == "" {
		project = "(unknown)"
	}
	project = layout.RenderTitleIcon(project)
	profile := res.Meta.DisplayProfile()
	profileLabel := sessionProfileLabel(res.Meta)
	if profileLabel == filters.profile {
		profileLabel = ""
	}
	harnessLabel := res.Meta.Harness
	if harnessLabel == filters.harness {
		harnessLabel = ""
	}
	var tagParts []string
	if profileLabel != "" {
		tagParts = append(tagParts, profileLabel)
	}
	if harnessLabel != "" {
		tagParts = append(tagParts, harnessLabel)
	}

	var matchFrag string
	if res.Matches > 0 {
		matchFrag = "  " + styleSearch.Render(fmt.Sprintf("(%d match%s)", res.Matches, matchPlural(res.Matches)))
	}

	// A search match is what the row is in the list for, so it keeps the
	// preview slot. An unfiltered list (no matches counted; its preview is
	// the summary) follows the summary with how the conversation ended,
	// which is what tells apart runs started from the same prompt.
	preview, ending := res.Preview, ""
	if preview == "" {
		preview = res.Meta.Summary()
	}
	if res.Matches == 0 {
		ending = res.Meta.Ending
		home, _ := os.UserHomeDir()
		preview = transcript.ShortenSummary(preview, home)
	}

	body := "  " + when
	if filters.project == "" || res.Meta.Project != filters.project {
		body += "  " + project
	}
	if len(tagParts) > 0 {
		body += "  " + accountStyle(acctColor[profile]).Render("["+strings.Join(tagParts, "/")+"]")
	}
	body += matchFrag
	if preview != "" {
		body += "  " + styleDim.Render(layout.FitLeadAndTail(preview, ending, maxRunes(maxWidth-layout.StyledWidth(body)-2)))
	}
	return layout.StyledTruncate(body, maxWidth, "…")
}

func sessionProfileTag(meta transcript.SessionMeta) string {
	profile := sessionProfileLabel(meta)
	if profile == "" {
		return "[" + meta.Harness + "]"
	}
	return "[" + profile + "/" + meta.Harness + "]"
}

// sessionProfileLabel renders the profile column for a transcript: the display
// profile, suffixed with "+N" when the transcript was touched by more than one
// profile.
func sessionProfileLabel(meta transcript.SessionMeta) string {
	profile := meta.DisplayProfile()
	if profile == "" {
		return ""
	}
	profiles := meta.ProfileNames()
	if len(profiles) <= 1 {
		return profile
	}
	extra := len(profiles)
	for _, candidate := range profiles {
		if candidate == profile {
			extra--
			break
		}
	}
	if extra <= 0 {
		return profile
	}
	return fmt.Sprintf("%s+%d", profile, extra)
}

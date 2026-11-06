package dashboard

import (
	"fmt"
	"github.com/sglavoie/agentmux/internal/session"
)

// ReviewSummary is computed from every member, including folded sessions.
func ReviewSummary(rows []session.RunningSession) string {
	reviewed, remaining := false, 0
	for _, s := range rows {
		reviewed = reviewed || s.Review.Reviewed
		if !s.Review.Accounted {
			remaining++
		}
	}
	if !reviewed {
		return ""
	}
	if remaining == 0 {
		return "✓ Accounted for"
	}
	return fmt.Sprintf("%d needs review", remaining)
}

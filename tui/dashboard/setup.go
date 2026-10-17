package dashboard

import "github.com/sglavoie/agentmux/internal/config"

// EmptyGuidance names the next useful step without performing any setup or
// interpreting an unobserved health check as a failure.
type EmptyGuidance struct {
	Title   string
	Detail  string
	Blocked bool
}

// SetupGuidance uses only configuration and existing health observations. The
// renderer suppresses it during loading and when a filter hid live sessions.
func SetupGuidance(cfg *config.Config, issues []HealthIssue) EmptyGuidance {
	if cfg != nil {
		if len(cfg.Profiles) == 0 && len(cfg.Accounts) == 0 {
			return EmptyGuidance{Detail: "Add an agent profile.\namx config edit\nThen validate: amx config validate", Blocked: true}
		}
		if len(cfg.Projects) == 0 && len(cfg.Sessions) == 0 {
			return EmptyGuidance{Detail: "Add a project directory and its profile.\namx config edit\nThen validate: amx config validate", Blocked: true}
		}
	}
	for _, issue := range issues {
		if issue.State == HealthFailed && (issue.ID == HealthIssueTopology || issue.ID == HealthIssueDaemon) {
			return EmptyGuidance{Title: "Sessions are unavailable.", Detail: issue.Summary + " Open Show Health Details from the command palette for recovery steps.", Blocked: true}
		}
	}
	if cfg != nil {
		return EmptyGuidance{Detail: "Choose a project in the launcher."}
	}
	return EmptyGuidance{}
}

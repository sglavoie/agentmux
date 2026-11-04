package dashboard

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
)

// The last-resort qualifier tells two otherwise identical rows apart. A row without a backend may carry a numeric window ID; a tmux row uses
// its pane ID because its process-local window number is not useful to an operator.
func TestWindowQualifierNamesTheBackendPaneForPortableRows(t *testing.T) {
	unknownRow := session.RunningSession{WindowID: 42}
	if got := qualifierText(unknownRow, qualifierWindow); got != "#42" {
		t.Fatalf("unknown-backend qualifier = %q, want #42", got)
	}
	tmuxRow := session.RunningSession{WindowID: 1 << 41, Backend: runtimesession.BackendTmux, Pane: backend.PaneID("%3")}
	if got := qualifierText(tmuxRow, qualifierWindow); got != "tmux %3" {
		t.Fatalf("tmux qualifier = %q, want the backend pane", got)
	}
}

package dashboard

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
)

// The last-resort qualifier tells two otherwise identical rows apart. A Kitty
// row has a window number an operator can recognize; a tmux row's number is
// internal to this process, so its pane in tmux is shown instead.
func TestWindowQualifierNamesTheBackendPaneForPortableRows(t *testing.T) {
	kittyRow := session.RunningSession{WindowID: 42}
	if got := qualifierText(kittyRow, qualifierWindow); got != "#42" {
		t.Fatalf("Kitty qualifier = %q, want #42", got)
	}
	tmuxRow := session.RunningSession{WindowID: 1 << 41, Backend: runtimesession.BackendTmux, Pane: backend.PaneID("%3")}
	if got := qualifierText(tmuxRow, qualifierWindow); got != "tmux %3" {
		t.Fatalf("tmux qualifier = %q, want the backend pane", got)
	}
}

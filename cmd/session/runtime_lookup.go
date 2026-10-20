package session

import (
	"errors"
	"fmt"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
)

// errNoRuntimeSession reports that the runtime session a command named is not
// a running tmux session. Commands map it to the not_found error code.
var errNoRuntimeSession = errors.New("no running tmux session")

// runtimeSessionPanes returns the panes term lists that still carry the
// runtime session id, or an error wrapping errNoRuntimeSession when none
// does. Only a pane that carries id is ever handed back, so a command acting
// on the result never touches another session's pane.
func runtimeSessionPanes(term backend.Discoverer, id runtimesession.ID) ([]backend.Pane, error) {
	panes, err := term.Discover()
	if err != nil {
		return nil, err
	}
	var carrying []backend.Pane
	for _, pane := range panes {
		if pane.Vars[session.RuntimeSessionVar] == string(id) {
			carrying = append(carrying, pane)
		}
	}
	if len(carrying) == 0 {
		return nil, noRuntimeSession(id)
	}
	return carrying, nil
}

// noRuntimeSession is the not-found error for the runtime session id.
func noRuntimeSession(id runtimesession.ID) error {
	return fmt.Errorf("%w with runtime session %s", errNoRuntimeSession, id)
}

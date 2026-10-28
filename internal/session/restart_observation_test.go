package session

import (
	"errors"
	"testing"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/tmux/tmuxtest"
)

// Accept the close request without stopping the agent, as an asynchronous
// terminal may do. Discovery either keeps showing it or fails after the close.
type unconfirmedCloseBackend struct {
	backend.TargetLauncher
	closeRequested bool
	failDiscovery  bool
}

func (b *unconfirmedCloseBackend) Close(backend.Ref) error {
	b.closeRequested = true
	return nil
}

func (b *unconfirmedCloseBackend) Discover() ([]backend.Pane, error) {
	if b.closeRequested && b.failDiscovery {
		return nil, errors.New("terminal discovery unavailable")
	}
	return b.TargetLauncher.Discover()
}

func TestRestartKeepsOriginalLiveUntilCloseIsConfirmed(t *testing.T) {
	for name := range replacements(config.Session{}) {
		for _, failDiscovery := range []bool{false, true} {
			observation := "still present"
			if failDiscovery {
				observation = "discovery failed"
			}
			t.Run(name+"/"+observation, func(t *testing.T) {
				f := tmuxtest.New(t)
				cfg, project := tmuxTestConfig(t)
				original, err := LaunchOn(tmuxRuntime(f), project, cfg.Accounts[0], cfg)
				if err != nil {
					t.Fatal(err)
				}
				running := runningOnTmux(t, f, cfg)[0]
				term := &unconfirmedCloseBackend{TargetLauncher: f.Backend, failDiscovery: failDiscovery}
				rt := Runtime{Tmux: term, Registry: f.Registry}

				replaced, err := replacements(project)[name](rt, running, cfg)
				if err != nil {
					t.Fatalf("replacement launched but restart reported failure: %v", err)
				}
				if !term.closeRequested {
					t.Fatal("restart never requested closure of the original")
				}
				if after := runningOnTmux(t, f, cfg); len(after) != 2 {
					t.Fatalf("running sessions = %+v; want original and replacement", after)
				}
				registry := f.Load(t)
				for _, id := range []runtimesession.ID{original.RuntimeSessionID, replaced.RuntimeSessionID} {
					record, err := registry.Get(id)
					if err != nil {
						t.Fatal(err)
					}
					if record.State != runtimesession.StateLive {
						t.Errorf("session %s is still running but recorded as %s", id, record.State)
					}
				}
			})
		}
	}
}

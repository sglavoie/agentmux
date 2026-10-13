package commands

import (
	"context"
	"log/slog"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/hooks"
)

// Health refreshes the daemon and/or hook health cached on the model.
// Each section is only re-queried when the corresponding flag is set so
// the cheap daemon ping doesn't pull in the more expensive hooks scan on
// every tick. Errors are non-fatal — they collapse into the "degraded"
// pip rendering rather than tearing down the dashboard.
// hookEvidence carries the journal position between hook checks, so only the
// first one reads the whole diagnostic journal.
var hookEvidence = hooks.NewEvidenceTracker()

// hookHealthTimeout bounds one hook check. The dashboard starts no second
// check while one is out, so a check that never returned would end them all.
const hookHealthTimeout = 30 * time.Second

func Health(cfg *config.Config, amxBin string, checkDaemon, checkHooks bool) tea.Cmd {
	return func() tea.Msg {
		msg := HealthMsg{DaemonChecked: checkDaemon, HookChecked: checkHooks}
		if checkDaemon {
			h := ipc.NewClient().Health()
			msg.DaemonAlive = h.Alive
			if !h.Alive {
				if err := SpawnDaemonDetached(amxBin); err != nil {
					slog.Debug("tui: starting daemon", "err", err)
				}
			}
		}
		if checkHooks {
			hookBin, _ := hooks.ExpectedBinary(amxBin, HookLookPath)
			ctx, cancel := context.WithTimeout(context.Background(), hookHealthTimeout)
			summary, err := hookEvidence.OverallHookHealth(ctx, cfg, hookBin)
			cancel()
			if err != nil {
				msg.HookErr = err
			} else {
				msg.HookSummary = HookHealth{
					InSync:  summary.InSync,
					Drifted: summary.Drifted,
					Missing: summary.Missing,
				}
			}
		}
		return msg
	}
}

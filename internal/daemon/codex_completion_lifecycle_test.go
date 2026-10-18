package daemon

import (
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

// A managed isolated Codex keeps its terminal identity across /new and resume.
// Exercise the production reducer and screen probes, including an eight-hour
// observation gap. This is a lifecycle simulation, not a physical sleep trial.
func TestCodexCompletionSurvivesConversationChangesAndObservationGaps(t *testing.T) {
	conversation := "resumed"
	agent := probedCodex
	agent.payload = func(string) hookevent.HookPayload { return hookevent.HookPayload{SessionID: conversation} }
	eachProbedBackendOf(t, agent, func(t *testing.T, s *probedSession) {
		for _, name := range []string{"resumed", "after-new", "after-second-new", "resumed"} {
			conversation = name
			requireAccepted(t, s.hook("SessionStart"))
			now := codexStopped(t, s)
			s.showHistory(tmuxCapture(t, "codex_idle_completed_t0.txt"))
			for _, gap := range []time.Duration{time.Minute, 8 * time.Hour} {
				s.quietRound(now.Add(gap))
				// Reopening a viewer reads state; it does not acknowledge completion.
				for range 3 {
					s.h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: s.slot})
				}
				if got := s.icon(); got != "stop" {
					t.Fatalf("%s after %s: icon %q, want stop", name, gap, got)
				}
			}
			if !hasFact(slotFacts(s.h, s.slot), protocol.SourceHarness, "codex:"+name) {
				t.Fatalf("completion belongs to another conversation: %+v", slotFacts(s.h, s.slot))
			}
		}
	})
}

func TestCodexCompletionAfterNewSurvivesDaemonRestart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		path := filepath.Join(t.TempDir(), runtimeCheckpointFileName)
		conversation := "resumed"
		agent := probedCodex
		agent.payload = func(string) hookevent.HookPayload { return hookevent.HookPayload{SessionID: conversation} }
		session, _ := newProbedTmuxSessionOf(t, agent)
		session.h.SetRuntimeCheckpointPath(path)
		for _, name := range []string{"resumed", "after-new"} {
			conversation = name
			requireAccepted(t, session.hook("SessionStart"))
			codexStopped(t, session)
		}
		runtime := runtimesession.ID(reducerRuntime(session.h, session.slot))
		if err := session.h.flushRuntimeCheckpoint(); err != nil {
			t.Fatal(err)
		}
		directory := newFakeRuntimeDirectory()
		directory.put(runtime, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, probedGeneration, "%7")
		after := checkpointHandler(t, path, directory)
		report := after.restoreRuntimeCheckpoint(time.Now())
		if report.Outcomes[string(runtime)] != runtimeRestoreRestored {
			t.Fatalf("restore = %+v", report)
		}
		slot := slotOf(t, after, runtime)
		effective, _ := windowEffective(t, after, slot)
		if effective.Icon != "stop" || !hasFact(slotFacts(after, slot), protocol.SourceHarness, "codex:after-new") {
			t.Fatalf("restored completion = %+v, facts %+v", effective, slotFacts(after, slot))
		}
	})
}

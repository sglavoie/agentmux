package daemon

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/companion"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

func TestCodexPromptProbeUnchangedPromptKeepsWaitingIdentity(t *testing.T) {
	command := readCodexCapture(t, "codex_prompt_command_approval.txt")
	eachProbedTmuxOf(t, probedCodex, func(t *testing.T, s *probedSession) {
		// These are the companion fields WorkspaceInspector.waiting uses for
		// key/answer eligibility, category, changedAt and contextVersion. ASL
		// question ID/count are absent for this ordinary Codex prompt.
		type waitingIdentity struct {
			runtime, backend, icon, reason, aslRun string
			presentation                           int64
		}
		identity := func(session protocol.CompanionSession) waitingIdentity {
			if session.Runtime == nil || session.Runtime.SessionID == "" || !session.Attention || session.PresentationChangedAtMS == 0 {
				t.Fatalf("not a presented waiting runtime: %+v", session)
			}
			return waitingIdentity{
				runtime: session.Runtime.SessionID, backend: session.Runtime.Backend,
				icon: session.IconState, reason: session.Reason, aslRun: session.ASLRunID,
				presentation: session.PresentationChangedAtMS,
			}
		}
		project := func() protocol.CompanionSession {
			s.h.stateMu.Lock()
			in := s.h.copyCompanionStateLocked(time.Now())
			s.h.stateMu.Unlock()
			snapshot := companion.Project(in)
			if len(snapshot.Sessions) != 1 || len(snapshot.ASL.Waiting) != 0 {
				t.Fatalf("want one ordinary prompt session, got %+v", snapshot)
			}
			return snapshot.Sessions[0]
		}

		s.startTurn()
		s.show(command)
		s.tick(time.Now())
		first := project()
		if first.IconState != "key" || first.Reason != "command_approval" {
			t.Fatalf("command prompt was not applied: %+v", first)
		}
		for range 3 {
			time.Sleep(2 * time.Second) // virtual time in eachProbedTmuxOf's synctest
			s.tick(time.Now())
			next := project()
			if next.LastEventAt <= first.LastEventAt {
				t.Fatalf("probe did not refresh activity: %d -> %d", first.LastEventAt, next.LastEventAt)
			}
			if identity(next) != identity(first) {
				t.Fatalf("unchanged screen changed waiting identity: %+v -> %+v", identity(first), identity(next))
			}
		}

		// Another kind of approval has the same icon and presentation time.
		// Its projected reason must still invalidate the captured question.
		s.show("Permission grant\nGrant permission for Codex to run commands in this workspace?\n› 1. Allow for this session\n  2. Ask each time\nPress enter to confirm")
		time.Sleep(2 * time.Second)
		s.tick(time.Now())
		changed := project()
		if changed.IconState != first.IconState || changed.Reason != "permission_grant" || changed.PresentationChangedAtMS != first.PresentationChangedAtMS {
			t.Fatalf("expected a different approval kind at the same presentation: %+v", changed)
		}
		if identity(changed) == identity(first) {
			t.Fatal("a changed classified prompt retained the old waiting identity")
		}
	})
}

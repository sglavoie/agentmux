package dashboard

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/session"
)

func TestRunningSessionDisplayTitleStripsLegacyPrefix(t *testing.T) {
	sess := session.RunningSession{Base: "W:api", Account: "work"}
	if got := SessionDisplayTitle(sess); got != "api" {
		t.Fatalf("SessionDisplayTitle() = %q, want %q", got, "api")
	}
}

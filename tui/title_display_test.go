package tui

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/session"
)

func TestRunningSessionDisplayTitleStripsLegacyPrefix(t *testing.T) {
	sess := session.RunningSession{Base: "W:api", Account: "work"}
	if got := runningSessionDisplayTitle(sess); got != "api" {
		t.Fatalf("runningSessionDisplayTitle() = %q, want %q", got, "api")
	}
}

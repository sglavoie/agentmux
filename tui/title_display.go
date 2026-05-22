package tui

import (
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
)

func runningSessionDisplayTitle(sess session.RunningSession) string {
	if title := sess.EffectiveProject(); title != "" {
		return title
	}
	return sess.Base
}

func configSessionDisplayTitle(sess config.Session) string {
	return sess.Title
}

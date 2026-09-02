package tui

import "github.com/sglavoie/agentmux/internal/config"

func configSessionDisplayTitle(sess config.Session) string {
	return sess.Title
}

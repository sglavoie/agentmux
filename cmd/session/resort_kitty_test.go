package session

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	agentmuxerrors "github.com/sglavoie/agentmux/internal/errors"
	"github.com/sglavoie/agentmux/internal/kitty"
)

func TestResortOutsideKittySaysSoPlainly(t *testing.T) {
	resetResortCommandTestState(t)
	resortLoadConfig = func() (*config.Config, error) { return &config.Config{}, nil }
	resortNewClient = func() kitty.KittyClient { return nil }
	resortSessions = func(kitty.KittyClient, string) error {
		return &agentmuxerrors.KittyError{Command: []string{"kitten", "@", "ls"}, Err: kitty.ErrNoKittyTerminal}
	}
	err := resortCmd.RunE(resortCmd, nil)
	if err == nil || strings.Contains(err.Error(), "kitten") || !strings.Contains(err.Error(), kitty.UnreachableMessage) {
		t.Fatalf("error = %v; want the plain Kitty wording", err)
	}
}

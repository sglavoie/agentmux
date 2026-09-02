package session

import (
	"bytes"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
)

func resetResortCommandTestState(t *testing.T) {
	t.Helper()
	oldLoadConfig := resortLoadConfig
	oldNewClient := resortNewClient
	oldResortSessions := resortSessions
	oldOut := resortCmd.OutOrStdout()
	t.Cleanup(func() {
		resortLoadConfig = oldLoadConfig
		resortNewClient = oldNewClient
		resortSessions = oldResortSessions
		resortCmd.SetOut(oldOut)
	})
}

func TestResortCommandWritesToCommandOutput(t *testing.T) {
	resetResortCommandTestState(t)

	resortLoadConfig = func() (*config.Config, error) {
		return &config.Config{Defaults: config.Defaults{Sort: "account"}}, nil
	}
	resortNewClient = func() kitty.KittyClient { return nil }
	var gotMode string
	resortSessions = func(_ kitty.KittyClient, mode string) error {
		gotMode = mode
		return nil
	}

	var out bytes.Buffer
	resortCmd.SetOut(&out)

	if err := resortCmd.RunE(resortCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if got, want := out.String(), "tabs resorted\n"; got != want {
		t.Fatalf("output = %q; want %q", got, want)
	}
	if gotMode != "account" {
		t.Fatalf("resort mode = %q; want account", gotMode)
	}
}

func TestResortCommandReturnsWriteError(t *testing.T) {
	resetResortCommandTestState(t)

	resortLoadConfig = func() (*config.Config, error) {
		return &config.Config{}, nil
	}
	resortNewClient = func() kitty.KittyClient { return nil }
	resortSessions = func(_ kitty.KittyClient, _ string) error {
		return nil
	}
	resortCmd.SetOut(failingWriter{})

	if err := resortCmd.RunE(resortCmd, nil); err == nil {
		t.Fatal("RunE error = nil; want write error")
	}
}

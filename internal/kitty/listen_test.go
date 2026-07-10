package kitty_test

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/kitty"
)

func TestNormalizeListenAddress(t *testing.T) {
	cases := []struct {
		name     string
		addr     string
		kittyPID string
		want     string
	}{
		{name: "unix passes through unchanged", addr: "unix:/tmp/kitty-123", kittyPID: "999", want: "unix:/tmp/kitty-123"},
		{name: "tcp passes through unchanged", addr: "tcp:localhost:4321", kittyPID: "999", want: "tcp:localhost:4321"},
		{name: "unix trimmed", addr: "  unix:/tmp/kitty-7  ", kittyPID: "", want: "unix:/tmp/kitty-7"},
		{name: "fd with valid pid yields stable unix socket", addr: "fd:34", kittyPID: "4242", want: "unix:/tmp/kitty-4242"},
		{name: "fd with padded pid trims", addr: "fd:0", kittyPID: " 88 ", want: "unix:/tmp/kitty-88"},
		{name: "fd with missing pid yields nothing", addr: "fd:34", kittyPID: "", want: ""},
		{name: "fd with non-numeric pid yields nothing", addr: "fd:34", kittyPID: "abc", want: ""},
		{name: "fd with zero pid yields nothing", addr: "fd:34", kittyPID: "0", want: ""},
		{name: "fd with negative pid yields nothing", addr: "fd:34", kittyPID: "-5", want: ""},
		{name: "malformed fd descriptor yields nothing", addr: "fd:notanumber", kittyPID: "4242", want: ""},
		{name: "negative fd descriptor yields nothing", addr: "fd:-1", kittyPID: "4242", want: ""},
		{name: "empty address yields nothing", addr: "", kittyPID: "4242", want: ""},
		{name: "prefix-only unix yields nothing", addr: "unix:", kittyPID: "4242", want: ""},
		{name: "prefix-only tcp yields nothing", addr: "tcp:", kittyPID: "4242", want: ""},
		{name: "unix with only whitespace endpoint yields nothing", addr: "unix:   ", kittyPID: "4242", want: ""},
		{name: "tcp with only whitespace endpoint yields nothing", addr: "tcp:  ", kittyPID: "4242", want: ""},
		{name: "unknown scheme is never guessed", addr: "/tmp/kitty-123", kittyPID: "4242", want: ""},
		{name: "unknown prefix is never guessed", addr: "socket:/tmp/kitty", kittyPID: "4242", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := kitty.NormalizeListenAddress(tc.addr, tc.kittyPID); got != tc.want {
				t.Fatalf("NormalizeListenAddress(%q, %q) = %q, want %q", tc.addr, tc.kittyPID, got, tc.want)
			}
		})
	}
}

func TestIsReusableListenAddress(t *testing.T) {
	cases := map[string]bool{
		"unix:/tmp/kitty-1":  true,
		"tcp:localhost:4321": true,
		" unix:/tmp/k ":      true,
		"unix:":              false,
		"tcp:":               false,
		"unix:   ":           false,
		"tcp:  ":             false,
		"fd:34":              false,
		"":                   false,
		"   ":                false,
		"/tmp/kitty-guess":   false,
		"socket:/tmp/kitty":  false,
	}
	for addr, want := range cases {
		if got := kitty.IsReusableListenAddress(addr); got != want {
			t.Errorf("IsReusableListenAddress(%q) = %v, want %v", addr, got, want)
		}
	}
}

func TestNormalizedListenAddressFromEnv(t *testing.T) {
	t.Run("fd descriptor uses KITTY_PID", func(t *testing.T) {
		t.Setenv("KITTY_LISTEN_ON", "fd:34")
		t.Setenv("KITTY_PID", "5150")
		if got := kitty.NormalizedListenAddressFromEnv(); got != "unix:/tmp/kitty-5150" {
			t.Fatalf("NormalizedListenAddressFromEnv() = %q, want unix:/tmp/kitty-5150", got)
		}
	})

	t.Run("fd descriptor without pid yields nothing", func(t *testing.T) {
		t.Setenv("KITTY_LISTEN_ON", "fd:34")
		t.Setenv("KITTY_PID", "")
		if got := kitty.NormalizedListenAddressFromEnv(); got != "" {
			t.Fatalf("NormalizedListenAddressFromEnv() = %q, want empty", got)
		}
	})

	t.Run("stable unix endpoint passes through", func(t *testing.T) {
		t.Setenv("KITTY_LISTEN_ON", "unix:/tmp/kitty-99")
		t.Setenv("KITTY_PID", "5150")
		if got := kitty.NormalizedListenAddressFromEnv(); got != "unix:/tmp/kitty-99" {
			t.Fatalf("NormalizedListenAddressFromEnv() = %q, want unix:/tmp/kitty-99", got)
		}
	})

	t.Run("empty outside kitty yields nothing", func(t *testing.T) {
		t.Setenv("KITTY_LISTEN_ON", "")
		t.Setenv("KITTY_PID", "")
		if got := kitty.NormalizedListenAddressFromEnv(); got != "" {
			t.Fatalf("NormalizedListenAddressFromEnv() = %q, want empty", got)
		}
	})
}

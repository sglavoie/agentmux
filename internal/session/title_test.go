package session_test

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
)

func TestAccountPrefix(t *testing.T) {
	cases := []struct {
		account string
		want    string
	}{
		{"work", "W:"},
		{"personal", "P:"},
		{"", ""},
		{"alice", "A:"},
	}
	for _, tc := range cases {
		got := session.AccountPrefix(tc.account)
		if got != tc.want {
			t.Errorf("AccountPrefix(%q) = %q, want %q", tc.account, got, tc.want)
		}
	}
}

func TestFormatTitle(t *testing.T) {
	cases := []struct {
		sess config.Session
		want string
	}{
		{config.Session{Title: "api", Account: "work"}, "W:api"},
		{config.Session{Title: "dotfiles", Account: "personal"}, "P:dotfiles"},
		{config.Session{Title: "myproject", Account: ""}, "myproject"},
	}
	for _, tc := range cases {
		got := session.FormatTitle(tc.sess)
		if got != tc.want {
			t.Errorf("FormatTitle({%q, %q}) = %q, want %q",
				tc.sess.Account, tc.sess.Title, got, tc.want)
		}
	}
}

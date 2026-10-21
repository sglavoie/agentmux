package session_test

import (
	"testing"
	"unicode/utf8"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
)

func TestAccountPrefix(t *testing.T) {
	t.Parallel()

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

// TestProfilePrefix_Unicode pins the multibyte-first-rune contract: a profile
// name whose first character spans more than one UTF-8 byte must yield a
// valid UTF-8 prefix, not a truncated invalid byte that corrupts the tab
// title rendered by Kitty.
func TestProfilePrefix_Unicode(t *testing.T) {
	t.Parallel()

	cases := []struct {
		profile string
		want    string
	}{
		{"éscriba", "É:"}, // 2-byte rune
		{"中文", "中:"},      // 3-byte rune; ToUpper is a no-op
		{"🐙ctopus", "🐙:"}, // 4-byte rune
	}
	for _, tc := range cases {
		got := session.ProfilePrefix(tc.profile)
		if got != tc.want {
			t.Errorf("ProfilePrefix(%q) = %q, want %q", tc.profile, got, tc.want)
		}
		if !utf8.ValidString(got) {
			t.Errorf("ProfilePrefix(%q) returned invalid UTF-8 %q", tc.profile, got)
		}
	}
}

func TestFormatTitle(t *testing.T) {
	t.Parallel()

	acc := config.Account{Name: "work", Color: "blue"}
	cfg := &config.Config{}
	cases := []struct {
		sess config.Session
		want string
	}{
		{config.Session{Title: "api", Account: "work"}, "api"},
		{config.Session{Title: "dotfiles", Account: "personal"}, "dotfiles"},
		{config.Session{Title: "api", Profile: "alpha", Account: "beta"}, "api"},
		{config.Session{Title: "myproject", Account: ""}, "myproject"},
	}
	for _, tc := range cases {
		got := session.FormatTitle(tc.sess, acc, cfg)
		if got != tc.want {
			t.Errorf("FormatTitle(Title=%q, Profile=%q, Account=%q) = %q, want %q",
				tc.sess.Title, tc.sess.Profile, tc.sess.Account, got, tc.want)
		}
	}
}

// TestFormatTitle_WithEmoji exercises the harness-emoji prefix path: when
// [harness_emojis] is configured the FormatTitle output prepends the emoji
// immediately before the project name. When the table is empty (or the
// harness is missing), the output equals sess.Title.
func TestFormatTitle_WithEmoji(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		acc  config.Account
		cfg  *config.Config
		sess config.Session
		want string
	}{
		{
			name: "claude emoji configured",
			acc:  config.Account{Name: "work", Color: "blue", Harness: "claude"},
			cfg:  &config.Config{HarnessEmojis: map[string]string{"claude": "🤖"}},
			sess: config.Session{Title: "api", Account: "work"},
			want: "🤖api",
		},
		{
			name: "harness without entry returns bare title",
			acc:  config.Account{Name: "work", Color: "blue", Harness: "claude"},
			cfg:  &config.Config{HarnessEmojis: map[string]string{"codex": "🦊"}},
			sess: config.Session{Title: "api", Account: "work"},
			want: "api",
		},
		{
			name: "empty HarnessEmojis returns bare title",
			acc:  config.Account{Name: "work", Color: "blue", Harness: "claude"},
			cfg:  &config.Config{},
			sess: config.Session{Title: "api", Account: "work"},
			want: "api",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := session.FormatTitle(tc.sess, tc.acc, tc.cfg)
			if got != tc.want {
				t.Errorf("FormatTitle = %q, want %q", got, tc.want)
			}
		})
	}
}

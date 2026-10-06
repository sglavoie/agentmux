package config_test

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/palette"
)

// TestValidColorIsThePalette pins the palette as the one list of colors: every
// palette name validates, a name outside it does not, and the error a typo
// produces lists exactly the palette's names.
func TestValidColorIsThePalette(t *testing.T) {
	for _, name := range palette.Names() {
		if !config.ValidColor(name) {
			t.Errorf("palette color %q rejected by ValidColor", name)
		}
	}
	if config.ValidColor("magenta") {
		t.Error("ValidColor accepted a name the palette does not define")
	}

	cfg := &config.Config{
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Profiles:      []config.Account{{Name: "work", Harness: "claude", HomeDir: "/tmp/h", Color: "magenta"}},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("invalid color accepted")
	}
	if want := "valid colors: " + strings.Join(palette.Names(), ", "); !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not list the palette (%q)", err, want)
	}
}

package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
)

// A load failure has to say it was a load failure. Commands print this straight
// to the operator, and "loading config: ..." is what distinguishes an unreadable
// or unparsable file from a file that parsed but does not describe a usable
// setup.
func TestLoadAndValidateWrapsLoadFailures(t *testing.T) {
	cause := errors.New("open config.toml: no such file or directory")

	cfg, err := loadAndValidate(func() (*config.Config, error) { return nil, cause })

	if cfg != nil {
		t.Fatalf("cfg = %+v; want nil on a load failure", cfg)
	}
	if err == nil || !strings.HasPrefix(err.Error(), "loading config: ") {
		t.Fatalf("err = %v; want a wrapped load failure", err)
	}
	if !errors.Is(err, cause) {
		t.Fatal("the underlying load error was not preserved for errors.Is")
	}
}

// The validation error reaches the operator unwrapped: it already names every
// broken rule, one per line, and a "loading config:" prefix in front of a
// multi-line report would misattribute it to the read.
func TestLoadAndValidateReturnsValidationErrorsUnwrapped(t *testing.T) {
	invalid := &config.Config{
		Projects: []config.Session{{Title: "api", Directory: "/nowhere", Account: "missing-profile"}},
	}

	cfg, err := loadAndValidate(func() (*config.Config, error) { return invalid, nil })

	if cfg != nil {
		t.Fatalf("cfg = %+v; want nil when the config is not runnable", cfg)
	}
	if err == nil {
		t.Fatal("an unrunnable config was accepted")
	}
	if strings.Contains(err.Error(), "loading config") {
		t.Fatalf("err = %v; a validation failure must not read as a load failure", err)
	}
}

// A config that loads and validates comes back as the very same value the
// loader produced: this boundary gates, it does not rewrite.
func TestLoadAndValidateReturnsTheLoadedConfig(t *testing.T) {
	loaded := &config.Config{}

	cfg, err := loadAndValidate(func() (*config.Config, error) { return loaded, nil })
	if err != nil {
		t.Fatalf("loadAndValidate: %v", err)
	}
	if cfg != loaded {
		t.Fatalf("cfg = %p; want the loaded config %p", cfg, loaded)
	}
}

func TestUnknownProfileNamesTheConfiguredProfiles(t *testing.T) {
	cfg := &config.Config{Profiles: []config.Account{{Name: "work"}, {Name: "codex-work"}}}
	if got, want := UnknownProfile(cfg, "home").Error(), `--profile "home": want one of work, codex-work`; got != want {
		t.Fatalf("UnknownProfile = %q, want %q", got, want)
	}
	if got, want := UnknownProfile(&config.Config{}, "home").Error(), `--profile "home": no profile is configured`; got != want {
		t.Fatalf("UnknownProfile without profiles = %q, want %q", got, want)
	}
}

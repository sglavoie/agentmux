package embed

import (
	"testing"

	"github.com/BurntSushi/toml"
)

func TestExampleConfigIsImmutableString(t *testing.T) {
	if ExampleConfig == "" {
		t.Fatal("ExampleConfig is empty")
	}
}

func TestExampleConfigIsValidTOML(t *testing.T) {
	var decoded map[string]any
	if _, err := toml.Decode(ExampleConfig, &decoded); err != nil {
		t.Fatalf("ExampleConfig is not valid TOML: %v", err)
	}

	if _, ok := decoded["defaults"]; !ok {
		t.Fatal("ExampleConfig is missing [defaults]")
	}
}

func TestExampleConfigIncludesStopFailureNotificationToggle(t *testing.T) {
	var decoded struct {
		Defaults struct {
			Notifications struct {
				StopFailure *bool `toml:"stop_failure"`
			} `toml:"notifications"`
		} `toml:"defaults"`
	}
	if _, err := toml.Decode(ExampleConfig, &decoded); err != nil {
		t.Fatalf("ExampleConfig is not valid TOML: %v", err)
	}
	if decoded.Defaults.Notifications.StopFailure == nil {
		t.Fatal("ExampleConfig is missing defaults.notifications.stop_failure")
	}
	if !*decoded.Defaults.Notifications.StopFailure {
		t.Fatal("defaults.notifications.stop_failure = false, want true")
	}
}

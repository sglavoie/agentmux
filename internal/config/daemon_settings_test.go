package config

import (
	"reflect"
	"testing"
)

func TestDaemonSettingsIgnoreLauncherEditsAndCompareEffectiveValues(t *testing.T) {
	base, err := parseConfig([]byte("[defaults]\nlaunch_backend='tmux'\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, body string
		changed    []string
	}{
		{"comments", "# edited\n[defaults]\nlaunch_backend='tmux'\n", []string{}},
		{"launcher", "[defaults]\nlaunch_backend='kitty'\neditor='vim'\nargs=['--verbose']\n[[projects]]\ntitle='new'\ndirectory='/tmp/new'\n", []string{}},
		{"explicit defaults", "[defaults]\ngit_probe=true\ngit_probe_ttl=10\ngit_probe_timeout=2\n[defaults.notifications]\nelicitation=false\n", []string{}},
		{"notifications", "[defaults.notifications]\nstop=true\n", []string{"Notifications"}},
		{"watchdog", "[defaults.watchdog]\nactive=1200\n", []string{"Activity detection"}},
		{"journal", "[defaults.diagnostics]\njournal_keep_days=3\n", []string{"Daemon journal"}},
		{"native log", "[defaults.diagnostics]\nnative_keep_days=3\n", []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := parseConfig([]byte(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			changed, known := ChangedDaemonSettings(base.DaemonSettings(), cfg.DaemonSettings())
			if !known || !reflect.DeepEqual(changed, tc.changed) {
				t.Fatalf("changed=%v known=%v, want %v", changed, known, tc.changed)
			}
		})
	}
	if _, known := ChangedDaemonSettings(nil, base.DaemonSettings()); known {
		t.Fatal("old daemon treated as applied")
	}
	old := base.DaemonSettings()
	old["Notifications"] = "v2:unknown"
	if _, known := ChangedDaemonSettings(old, base.DaemonSettings()); known {
		t.Fatal("incompatible projection treated as applied")
	}
}

func TestDaemonSettingsNormalizeProfileOrderAndWatchdogInheritance(t *testing.T) {
	first, err := parseConfig([]byte("[[profiles]]\nname='a'\nharness='copilot'\nhome_dir='/profiles/a'\ncolor='blue'\n[[profiles]]\nname='b'\nharness='claude'\nhome_dir='/profiles/b'\ncolor='red'\n"))
	if err != nil {
		t.Fatal(err)
	}
	second := *first
	second.Profiles = []Account{first.Profiles[1], first.Profiles[0]}
	second.Profiles[0].Args = []string{"--verbose"}
	second.Profiles[0].Watchdog.Active = first.Defaults.Watchdog.Active
	if changed, known := ChangedDaemonSettings(first.DaemonSettings(), second.DaemonSettings()); !known || len(changed) != 0 {
		t.Fatalf("irrelevant profile edit changed %v", changed)
	}
	second.Profiles[0].Watchdog.Active++
	if changed, _ := ChangedDaemonSettings(first.DaemonSettings(), second.DaemonSettings()); !reflect.DeepEqual(changed, []string{"Activity detection"}) {
		t.Fatalf("profile watchdog edit changed %v", changed)
	}
}

package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOverviewOpensCompactUnlessConfiguredOtherwise(t *testing.T) {
	for _, tc := range []struct {
		body string
		want bool
	}{
		{body: "", want: true},
		{body: "[defaults.overview]\ncompact = true\n", want: true},
		{body: "[defaults.overview]\ncompact = false\n", want: false},
	} {
		path := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("load %q: %v", tc.body, err)
		}
		if got := cfg.Defaults.Overview.CompactValue(); got != tc.want {
			t.Errorf("config %q: compact=%v, want %v", tc.body, got, tc.want)
		}
	}
}

package config

import "testing"

func TestRecapProfileAndExplicitDisable(t *testing.T) {
	c := &Config{Profiles: []Account{{Name: "codex", Harness: "codex"}, {Name: "personal", Harness: "claude"}, {Name: "other", Harness: "claude"}}}
	p, err := c.RecapProfile()
	if err != nil || p.Name != "personal" || !c.Defaults.Recaps.EnabledValue() {
		t.Fatalf("default %+v, %v", p, err)
	}
	c.Defaults.Recaps.Profile = "other"
	p, err = c.RecapProfile()
	if err != nil || p.Name != "other" {
		t.Fatalf("selected %+v, %v", p, err)
	}
	c.Defaults.Recaps.Profile = "codex"
	if len(validateRecaps(c)) == 0 {
		t.Fatal("accepted unsupported summarizer")
	}
	c.Defaults.Recaps.Profile = "missing"
	if len(validateRecaps(c)) == 0 {
		t.Fatal("accepted nonexistent profile")
	}
	c.Defaults.Recaps.Profile = ""
	c.Defaults.Recaps.Model = "--resume"
	if len(validateRecaps(c)) == 0 {
		t.Fatal("accepted CLI option as model")
	}
	falseValue := false
	c.Defaults.Recaps.Enabled = &falseValue
	if c.Defaults.Recaps.EnabledValue() {
		t.Fatal("ignored explicit disable")
	}
}

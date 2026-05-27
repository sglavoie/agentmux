package palette

import "testing"

func TestStarIcon(t *testing.T) {
	if got := StarIcon(); got == "" {
		t.Error("StarIcon() is empty")
	}
}

func TestLockIcon(t *testing.T) {
	if got := LockIcon(); got != "🔒" {
		t.Errorf("LockIcon() = %q, want %q", got, "🔒")
	}
}

func TestStateIconAllStates(t *testing.T) {
	states := []string{"bolt", "stop", "key", "warning", "error", "subagent", "compact", "question"}
	for _, s := range states {
		if got := StateIcon(s); got == "" {
			t.Errorf("StateIcon(%q) is empty", s)
		}
	}
}

func TestStateIconQuestion(t *testing.T) {
	if got := StateIcon("question"); got != "❓" {
		t.Errorf("StateIcon(\"question\") = %q, want %q", got, "❓")
	}
}

func TestStateIconUnknown(t *testing.T) {
	if got := StateIcon("unknown"); got != "" {
		t.Errorf("StateIcon(\"unknown\") = %q, want empty", got)
	}
}

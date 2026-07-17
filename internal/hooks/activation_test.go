package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/diagnostics"
	"github.com/sglavoie/agentmux/internal/harness"
)

func stubActivationEvidence(t *testing.T, versions map[string]string) string {
	t.Helper()
	claudeRoot := t.TempDir()
	oldVersionProbe := activationVersionProbe
	oldEvidenceProbe := activationEvidenceProbe
	oldClaudeRoot := activationClaudeManagedRoot
	activationVersionProbe = func(_ context.Context, executable string) (string, error) {
		return versions[executable], nil
	}
	activationEvidenceProbe = func(_ context.Context, cfg *config.Config) (*StatusEvidence, error) {
		evidence := NewStatusEvidence()
		if cfg != nil {
			for _, account := range cfg.Accounts {
				_ = evidence.Visit(diagnostics.TraceEnvelope{
					Stage: "hook.test", Harness: account.EffectiveHarness(),
					Evidence:   map[string]string{"config_dir": config.ExpandTilde(account.EffectiveHomeDir())},
					ObservedAt: time.Now().UTC(),
				})
			}
		}
		return evidence, nil
	}
	activationClaudeManagedRoot = func() string { return claudeRoot }
	t.Cleanup(func() {
		activationVersionProbe = oldVersionProbe
		activationEvidenceProbe = oldEvidenceProbe
		activationClaudeManagedRoot = oldClaudeRoot
	})
	return claudeRoot
}

func hasActivationIssue(status AccountStatus, code string) bool {
	for _, issue := range status.ActivationIssues {
		if issue.Code == code {
			return true
		}
	}
	return false
}

func TestCodexDefinitionHashMatchesExactCodexTrustIdentity(t *testing.T) {
	timeout := uint64(5)
	handler := codexRawHookEntry{
		Type:    "command",
		Command: "/Users/sglavoie/.local/bin/amx hook-indicator PreToolUse --harness=codex",
		Timeout: &timeout,
	}
	want := "sha256:bea2cc6d752752b8fd50e18d97c32cc365068bf1dc8d586e9ea0a5b6774800f5"
	if got := codexDefinitionHash("PreToolUse", nil, handler); got != want {
		t.Fatalf("codexDefinitionHash = %q, want %q", got, want)
	}
}

func TestActivationCodexTrustBypassIsHealthy(t *testing.T) {
	stubActivationEvidence(t, map[string]string{"codex": "0.144.4"})
	home := t.TempDir()
	cfg := &config.Config{Accounts: []config.Account{{
		Name: "work", Harness: harness.Codex, HomeDir: home,
		Args: []string{"--dangerously-bypass-hook-trust"},
	}}}
	if _, err := MergeAll(cfg, "", testBin); err != nil {
		t.Fatalf("MergeAll: %v", err)
	}
	statuses, err := StatusAll(cfg, testBin)
	if err != nil {
		t.Fatalf("StatusAll: %v", err)
	}
	if got := statuses[0].Activation; got != ActivationHealthy {
		t.Fatalf("activation = %s, want healthy; issues=%+v", got, statuses[0].ActivationIssues)
	}
}

func TestActivationCodexFeatureDisableFailsDespiteTrustBypass(t *testing.T) {
	stubActivationEvidence(t, map[string]string{"codex": "0.144.4"})
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[features]\nhooks = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Accounts: []config.Account{{
		Name: "work", Harness: harness.Codex, HomeDir: home,
		Args: []string{"--dangerously-bypass-hook-trust"},
	}}}
	if _, err := MergeAll(cfg, "", testBin); err != nil {
		t.Fatalf("MergeAll: %v", err)
	}
	statuses, err := StatusAll(cfg, testBin)
	if err != nil {
		t.Fatalf("StatusAll: %v", err)
	}
	if statuses[0].Activation != ActivationFailure || !hasActivationIssue(statuses[0], "codex_hooks_disabled") {
		t.Fatalf("status = %+v, want codex_hooks_disabled failure", statuses[0])
	}
}

func TestActivationCodexMissingExactTrustFails(t *testing.T) {
	stubActivationEvidence(t, map[string]string{"codex": "0.144.4"})
	home := t.TempDir()
	cfg := &config.Config{Accounts: []config.Account{{Name: "work", Harness: harness.Codex, HomeDir: home}}}
	if _, err := MergeAll(cfg, "", testBin); err != nil {
		t.Fatalf("MergeAll: %v", err)
	}
	statuses, err := StatusAll(cfg, testBin)
	if err != nil {
		t.Fatalf("StatusAll: %v", err)
	}
	if statuses[0].Activation != ActivationFailure || !hasActivationIssue(statuses[0], "codex_hook_untrusted") {
		t.Fatalf("status = %+v, want exact-definition trust failure", statuses[0])
	}
}

func TestActivationCodexExactTrustIsHealthy(t *testing.T) {
	stubActivationEvidence(t, map[string]string{"codex": "0.144.4"})
	home := t.TempDir()
	cfg := &config.Config{Accounts: []config.Account{{Name: "work", Harness: harness.Codex, HomeDir: home}}}
	if _, err := MergeAll(cfg, "", testBin); err != nil {
		t.Fatalf("MergeAll: %v", err)
	}
	hooksPath := filepath.Join(home, "hooks.json")
	data, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Hooks map[string]json.RawMessage `json:"hooks"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	var trusted strings.Builder
	trusted.WriteString("[hooks.state]\n")
	for _, contract := range harness.HookEventMatrix(harness.Codex) {
		var groups []codexRawHookGroup
		if err := json.Unmarshal(file.Hooks[contract.CanonicalName], &groups); err != nil {
			t.Fatalf("parse %s: %v", contract.CanonicalName, err)
		}
		for groupIndex, group := range groups {
			for handlerIndex, handler := range group.Hooks {
				key := fmt.Sprintf("%s:%s:%d:%d", hooksPath, eventSnakeCase(contract.CanonicalName), groupIndex, handlerIndex)
				fmt.Fprintf(&trusted, "\n[hooks.state.%q]\ntrusted_hash = %q\n", key, codexDefinitionHash(contract.CanonicalName, group.Matcher, handler))
			}
		}
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(trusted.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	statuses, err := StatusAll(cfg, testBin)
	if err != nil {
		t.Fatalf("StatusAll: %v", err)
	}
	if statuses[0].Activation != ActivationHealthy {
		t.Fatalf("status = %+v, want exact-definition trust healthy", statuses[0])
	}
}

func TestActivationCodexUnavailableTrustEvidenceIsUnverified(t *testing.T) {
	stubActivationEvidence(t, map[string]string{"codex": "0.144.4"})
	home := t.TempDir()
	cfg := &config.Config{Accounts: []config.Account{{Name: "work", Harness: harness.Codex, HomeDir: home}}}
	if _, err := MergeAll(cfg, "", testBin); err != nil {
		t.Fatalf("MergeAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[hooks.state\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	statuses, err := StatusAll(cfg, testBin)
	if err != nil {
		t.Fatalf("StatusAll: %v", err)
	}
	if statuses[0].Activation != ActivationUnverified || !hasActivationIssue(statuses[0], "codex_config_unavailable") {
		t.Fatalf("status = %+v, want unavailable trust evidence to be unverified", statuses[0])
	}
}

func TestActivationCodexProfileOverlayCanDisableHooks(t *testing.T) {
	stubActivationEvidence(t, map[string]string{"codex": "0.144.4"})
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "locked.config.toml"), []byte("[features]\nhooks = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Accounts: []config.Account{{
		Name: "work", Harness: harness.Codex, HomeDir: home,
		Args: []string{"--profile", "locked", "--dangerously-bypass-hook-trust"},
	}}}
	if _, err := MergeAll(cfg, "", testBin); err != nil {
		t.Fatalf("MergeAll: %v", err)
	}
	statuses, err := StatusAll(cfg, testBin)
	if err != nil {
		t.Fatalf("StatusAll: %v", err)
	}
	if statuses[0].Activation != ActivationFailure || !hasActivationIssue(statuses[0], "codex_hooks_disabled") {
		t.Fatalf("status = %+v, want profile overlay disable failure", statuses[0])
	}
}

func TestActivationClaudeDisableAllHooksAndSafeModeFail(t *testing.T) {
	stubActivationEvidence(t, map[string]string{"claude": "2.1.195"})
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "settings.json"), []byte(`{"disableAllHooks":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Accounts: []config.Account{{
		Name: "work", Harness: harness.Claude, HomeDir: home, Args: []string{"--safe-mode"},
	}}}
	if _, err := MergeAll(cfg, "", testBin); err != nil {
		t.Fatalf("MergeAll: %v", err)
	}
	statuses, err := StatusAll(cfg, testBin)
	if err != nil {
		t.Fatalf("StatusAll: %v", err)
	}
	status := statuses[0]
	if status.Activation != ActivationFailure || !hasActivationIssue(status, "claude_hooks_disabled") || !hasActivationIssue(status, "claude_safe_mode") {
		t.Fatalf("status = %+v, want disableAllHooks and safe-mode failures", status)
	}
}

func TestActivationClaudeManagedHookRestrictionFails(t *testing.T) {
	managedRoot := stubActivationEvidence(t, map[string]string{"claude": "2.1.195"})
	if err := os.WriteFile(filepath.Join(managedRoot, "managed-settings.json"), []byte(`{"allowManagedHooksOnly":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	cfg := &config.Config{Accounts: []config.Account{{Name: "work", Harness: harness.Claude, HomeDir: home}}}
	if _, err := MergeAll(cfg, "", testBin); err != nil {
		t.Fatalf("MergeAll: %v", err)
	}
	statuses, err := StatusAll(cfg, testBin)
	if err != nil {
		t.Fatalf("StatusAll: %v", err)
	}
	if statuses[0].Activation != ActivationFailure || !hasActivationIssue(statuses[0], "managed_hooks_only") {
		t.Fatalf("status = %+v, want managed hook restriction failure", statuses[0])
	}
}

func TestActivationCopilotReadsUserSettingsLayer(t *testing.T) {
	stubActivationEvidence(t, map[string]string{"copilot": "1.0.70"})
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "settings.json"), []byte(`{"disableAllHooks":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Accounts: []config.Account{{Name: "work", Harness: harness.Copilot, HomeDir: home}}}
	if _, err := MergeAll(cfg, "", testBin); err != nil {
		t.Fatalf("MergeAll: %v", err)
	}
	statuses, err := StatusAll(cfg, testBin)
	if err != nil {
		t.Fatalf("StatusAll: %v", err)
	}
	if statuses[0].Activation != ActivationFailure || !hasActivationIssue(statuses[0], "copilot_hooks_disabled") {
		t.Fatalf("status = %+v, want copilot_hooks_disabled failure", statuses[0])
	}
}

func TestActivationCopilotReadsRepositoryLocalSettingsLayer(t *testing.T) {
	stubActivationEvidence(t, map[string]string{"copilot": "1.0.70"})
	home := t.TempDir()
	project := t.TempDir()
	settingsDir := filepath.Join(project, ".github", "copilot")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(settingsDir, "settings.local.json"), []byte(`{"disableAllHooks":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Accounts: []config.Account{{Name: "work", Harness: harness.Copilot, HomeDir: home}},
		Projects: []config.Session{{Title: "project", Directory: project, Profile: "work"}},
	}
	if _, err := MergeAll(cfg, "", testBin); err != nil {
		t.Fatalf("MergeAll: %v", err)
	}
	statuses, err := StatusAll(cfg, testBin)
	if err != nil {
		t.Fatalf("StatusAll: %v", err)
	}
	if statuses[0].Activation != ActivationFailure || !hasActivationIssue(statuses[0], "copilot_hooks_disabled") {
		t.Fatalf("status = %+v, want repository-local disableAllHooks failure", statuses[0])
	}
}

func TestCopilotWorkingDirectoryResolvesRelativeLaunchOverride(t *testing.T) {
	want := filepath.Join("/repo", "nested")
	if got := copilotWorkingDirectory("/repo", []string{"-C", "nested"}); got != want {
		t.Fatalf("copilotWorkingDirectory = %q, want %q", got, want)
	}
}

func TestActivationDisabledAgentMuxEventsFail(t *testing.T) {
	stubActivationEvidence(t, map[string]string{"claude": "2.1.195"})
	home := t.TempDir()
	cfg := &config.Config{
		Accounts: []config.Account{{Name: "work", Harness: harness.Claude, HomeDir: home}},
		Hooks: config.HooksConfig{Overrides: map[string]config.AccountHookOverride{
			"work": {DisabledEvents: []string{"PreToolUse"}},
		}},
	}
	if _, err := MergeAll(cfg, "", testBin); err != nil {
		t.Fatalf("MergeAll: %v", err)
	}
	statuses, err := StatusAll(cfg, testBin)
	if err != nil {
		t.Fatalf("StatusAll: %v", err)
	}
	if statuses[0].Activation != ActivationFailure || !hasActivationIssue(statuses[0], "events_disabled") {
		t.Fatalf("status = %+v, want disabled Agent Mux event failure", statuses[0])
	}
}

func TestActivationNewerCLIIsUnverifiedNotHealthy(t *testing.T) {
	stubActivationEvidence(t, map[string]string{"claude": "2.1.196"})
	home := t.TempDir()
	cfg := &config.Config{Accounts: []config.Account{{Name: "work", Harness: harness.Claude, HomeDir: home}}}
	if _, err := MergeAll(cfg, "", testBin); err != nil {
		t.Fatalf("MergeAll: %v", err)
	}
	statuses, err := StatusAll(cfg, testBin)
	if err != nil {
		t.Fatalf("StatusAll: %v", err)
	}
	if statuses[0].Activation != ActivationUnverified || !hasActivationIssue(statuses[0], "contract_stale") {
		t.Fatalf("status = %+v, want contract_stale unverified", statuses[0])
	}
}

func TestStaleLastSeenEvidenceIsUnverifiedNotBroken(t *testing.T) {
	stubActivationEvidence(t, map[string]string{"claude": "2.1.195"})
	activationEvidenceProbe = func(_ context.Context, cfg *config.Config) (*StatusEvidence, error) {
		evidence := NewStatusEvidence()
		for _, account := range cfg.Accounts {
			_ = evidence.Visit(diagnostics.TraceEnvelope{
				Stage: "hook.test", Harness: account.EffectiveHarness(), Profile: account.Name, ObservedAt: time.Now().Add(-8 * time.Hour),
			})
		}
		return evidence, nil
	}
	home := t.TempDir()
	cfg := &config.Config{Accounts: []config.Account{{Name: "work", Harness: harness.Claude, HomeDir: home}}}
	if _, err := MergeAll(cfg, "", testBin); err != nil {
		t.Fatalf("MergeAll: %v", err)
	}
	statuses, err := StatusAll(cfg, testBin)
	if err != nil {
		t.Fatalf("StatusAll: %v", err)
	}
	if statuses[0].Activation != ActivationUnverified || !hasActivationIssue(statuses[0], "last_seen_stale") {
		t.Fatalf("status = %+v, want stale observation to be unverified", statuses[0])
	}
}

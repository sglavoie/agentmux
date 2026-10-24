package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfirmationReportsFreshConfigAndRejectsUnknownPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	t.Setenv("AMX_CONFIG", path)
	oldOut := confirmationCmd.OutOrStdout()
	t.Cleanup(func() {
		confirmationCmd.SetOut(oldOut)
		_ = confirmationCmd.Flags().Set("json", "false")
		_ = confirmationCmd.Flags().Set("policy", "follow-cli")
	})
	_ = confirmationCmd.Flags().Set("json", "true")
	for _, required := range []bool{true, false} {
		value := "false"
		if required {
			value = "true"
		}
		if err := os.WriteFile(path, []byte("[defaults]\nconfirm_destructive = "+value+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		confirmationCmd.SetOut(&out)
		if err := confirmationCmd.RunE(confirmationCmd, nil); err != nil {
			t.Fatal(err)
		}
		var result struct {
			Data struct {
				Required bool `json:"required"`
			} `json:"data"`
		}
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Data.Required != required {
			t.Fatalf("required=%t want %t", result.Data.Required, required)
		}
	}
	// An unknown policy is the caller's mistake, not an unavailable answer.
	_ = confirmationCmd.Flags().Set("policy", "typo")
	var out bytes.Buffer
	confirmationCmd.SetOut(&out)
	if err := confirmationCmd.RunE(confirmationCmd, nil); err == nil {
		t.Fatal("unknown policy accepted")
	}
	if !strings.Contains(out.String(), `"code":"invalid_arguments"`) || !strings.Contains(out.String(), "--policy") {
		t.Fatalf("unknown policy output = %s", out.String())
	}
}

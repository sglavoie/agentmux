package session

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func TestHistoryJSONPagesScopeAndHiddenMatches(t *testing.T) {
	resetSessionCommandsTestState(t)
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	messages := map[string][]transcript.Message{}
	metas := []transcript.SessionMeta{}
	for i := range 123 {
		id := fmt.Sprintf("session-%03d", i)
		path := filepath.Join(dir, id)
		if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		metas = append(metas, transcript.SessionMeta{ID: id, Harness: "claude", Path: path, CWD: dir,
			FileSize: info.Size(), FileModTime: info.ModTime(), LastAt: time.Unix(123, 0), StartedAt: time.Unix(120, 0)})
		messages[path] = []transcript.Message{{Role: "user", Text: "needle phrase"}}
	}
	metas[122] = automated(metas[122])
	sessionCommandsSessionIndexPath = func() string { return filepath.Join(dir, "index.json") }
	sessionCommandsLoadConfig = func() (*config.Config, error) {
		return &config.Config{Profiles: []config.Account{{Name: "work", Harness: "claude", HomeDir: dir}}}, nil
	}
	sessionCommandsSessionReaders = func() map[string]transcript.Reader {
		return map[string]transcript.Reader{"claude": searchCommandReader{sessionCommandReader{harness: "claude", discovered: metas}, messages}}
	}
	for _, command := range []*cobra.Command{sessionListCmd, sessionSearchCmd} {
		t.Run(command.Name(), func(t *testing.T) {
			previous := command.OutOrStdout()
			reset := func() {
				command.Flags().VisitAll(func(f *pflag.Flag) {
					if values, ok := f.Value.(pflag.SliceValue); ok {
						_ = values.Replace(nil)
					} else {
						_ = f.Value.Set(f.DefValue)
					}
					f.Changed = false
				})
			}
			t.Cleanup(func() { reset(); command.SetOut(previous) })
			run := func(offset int, exact string, all bool) map[string]json.RawMessage {
				reset()
				for name, value := range map[string]string{"json": "true", "offset": fmt.Sprint(offset), "limit": "50", "conversation": exact, "all": fmt.Sprint(all)} {
					if err := command.Flags().Set(name, value); err != nil {
						t.Fatal(err)
					}
				}
				var output bytes.Buffer
				command.SetOut(&output)
				args := []string{}
				if command == sessionSearchCmd {
					args = []string{`"needle phrase"`}
				}
				if err := command.RunE(command, args); err != nil {
					t.Fatal(err)
				}
				var envelope struct {
					Schema int                        `json:"schema_version"`
					Data   map[string]json.RawMessage `json:"data"`
				}
				if err := json.Unmarshal(output.Bytes(), &envelope); err != nil {
					t.Fatal(err)
				}
				if envelope.Schema != 1 {
					t.Fatal(envelope.Schema)
				}
				return envelope.Data
			}
			ids := []string{}
			key := "sessions"
			if command == sessionSearchCmd {
				key = "results"
			}
			for _, offset := range []int{0, 50, 100} {
				data := run(offset, "", false)
				var rows []sessionView
				if err := json.Unmarshal(data[key], &rows); err != nil {
					t.Fatal(err)
				}
				for _, row := range rows {
					ids = append(ids, row.ID)
				}
				if string(data["total"]) != "122" || string(data["hidden_automated"]) != "1" || string(data["has_more"]) != fmt.Sprint(offset < 100) {
					t.Fatalf("metadata: %s", data)
				}
			}
			if len(ids) != 122 {
				t.Fatalf("got %d rows", len(ids))
			}
			for i, id := range ids {
				if id != fmt.Sprintf("session-%03d", i) {
					t.Fatalf("unstable page %d: %s", i, id)
				}
			}
			exact := run(0, "claude:session-060", false)
			if string(exact["total"]) != "1" {
				t.Fatalf("exact scope: %s", exact)
			}
			if string(run(0, "codex:session-060", false)["total"]) != "0" {
				t.Fatal("cross-harness match")
			}
			hidden := run(0, "claude:session-122", false)
			if string(hidden["total"]) != "0" || string(hidden["hidden_automated"]) != "1" {
				t.Fatal("hidden count escaped scope")
			}
			if string(run(0, "claude:session-122", true)["total"]) != "1" {
				t.Fatal("automated session missing")
			}
			if command == sessionSearchCmd && string(exact["match_terms"]) != `["needle phrase"]` {
				t.Fatal("phrase highlighting differs from search")
			}
			if string(run(500, "", false)["has_more"]) != "false" {
				t.Fatal("past-end page")
			}
			for flag, value := range map[string]string{"offset": "-1", "conversation": "missing-harness"} {
				reset()
				_ = command.Flags().Set("json", "true")
				_ = command.Flags().Set(flag, value)
				if err := command.RunE(command, []string{"needle"}); err == nil {
					t.Fatalf("accepted %s", flag)
				}
			}
		})
	}
}

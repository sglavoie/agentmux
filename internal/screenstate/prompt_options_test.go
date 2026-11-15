package screenstate

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParsePromptFixtures(t *testing.T) {
	for _, tt := range []struct {
		file, question, summary, first, last string
	}{
		{"claude_permission_prompt.txt", "Do you want to create perm-demo.txt?", "Do you want to create perm-demo.txt?", "Yes", "No"},
		{"codex_prompt_command_approval.txt", "Would you like to run the following command?", "rm -f /tmp/amx-fixture-capture/perm-demo.txt", "Yes, proceed (y)", "No, and tell Codex what to do differently (esc)"},
		{"copilot_permission_prompt.txt", "Do you want to run this command?", "rm -f notes.txt", "Yes", "No, and tell Copilot what to do differently (Esc to stop)"},
		{"claude_idle_completed.txt", "", "", "", ""},
		{"codex_working.txt", "", "", "", ""},
	} {
		t.Run(tt.file, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", tt.file))
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(string(data), "\n")
			lines = lines[max(0, len(lines)-24):]
			got, ok := ParsePrompt(lines)
			if ok != (tt.first != "") {
				t.Fatalf("ParsePrompt = %+v, %v", got, ok)
			}
			if !ok {
				return
			}
			if got.Question != tt.question || got.Summary != tt.summary || len(got.Options) != 3 {
				t.Fatalf("ParsePrompt = %+v", got)
			}
			for i, option := range got.Options {
				if option.Key != string(rune('1'+i)) || option.Selected != (i == 0) {
					t.Errorf("option %d = %+v", i+1, option)
				}
			}
			if got.Options[0].Label != tt.first || got.Options[2].Label != tt.last {
				t.Errorf("options = %+v", got.Options)
			}
		})
	}
}

func TestParsePromptOptionBlocks(t *testing.T) {
	for _, tt := range []struct {
		name, text string
		want       []PromptOption
	}{
		{"wrapped second option", "1. Yes\n2. Yes, allow\n   this command again\n3. No", []PromptOption{{"1", "Yes", false}, {"2", "Yes, allow this command again", false}, {"3", "No", false}}},
		{"gap", "1. Yes\n3. No", nil},
		{"single", "1. Yes", nil},
		{"no options", "ordinary text", nil},
		{"missing first", "2. Yes\n3. No", nil},
		{"blank splits block", "1. Yes\n\n2. No", nil},
		{"separator splits block", "1. Yes\n╰──╯\n2. No", nil},
		{"duplicate", "1. Yes\n2. Maybe\n2. No", nil},
		{"footer is ignored", "1. Yes\n2. No\n\nordinary text below", []PromptOption{{"1", "Yes", false}, {"2", "No", false}}},
		{"later incomplete block wins", "1. Old yes\n2. Old no\n\n1. New choice\n\nordinary text below", nil},
		{"last block wins", "1. Old yes\n2. Old no\n\n> 1. New yes\n2. New no", []PromptOption{{"1", "New yes", true}, {"2", "New no", false}}},
		{"nested boxes and markers", "┃ │ ❯ 1. Yes │ ┃\n┃ │ › 2. No │ ┃", []PromptOption{{"1", "Yes", true}, {"2", "No", true}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParsePrompt(strings.Split(tt.text, "\n"))
			if ok != (tt.want != nil) || !reflect.DeepEqual(got.Options, tt.want) {
				t.Fatalf("ParsePrompt = %+v, %v; want %+v", got, ok, tt.want)
			}
		})
	}
}

func TestParsePromptContext(t *testing.T) {
	for _, tt := range []struct {
		name, context, question, summary string
	}{
		{"nearest question", "Old question?\nNew question?", "New question?", "New question?"},
		{"nearer command", "Run it?\n$ echo hello", "Run it?", "echo hello"},
		{"no question", "$ echo hello", "", "echo hello"},
		{"history command ignored", "$ Shell history\nrm file\nDo you want to run this command?", "Do you want to run this command?", "rm file"},
		{"generic case insensitive", "rm file\nDo You Want To Proceed?", "Do You Want To Proceed?", "rm file"},
		{"generic without preceding context", "Do you want to proceed?", "Do you want to proceed?", "Do you want to proceed?"},
		{"specific question", "rm file\nDelete file?", "Delete file?", "Delete file?"},
		{"skip boxes and blank lines", "rm file\n┌━═╌▀▄-╭╮╰╯┐└┘ ─┘\n\nDo you want to run this command?", "Do you want to run this command?", "rm file"},
		{"six context lines only", "Old question?\na\nb\nc\nd\ne\nf", "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParsePrompt(strings.Split(tt.context+"\n1. Yes\n2. No", "\n"))
			if !ok || got.Question != tt.question || got.Summary != tt.summary {
				t.Fatalf("ParsePrompt = %+v, %v; want question %q, summary %q", got, ok, tt.question, tt.summary)
			}
		})
	}
}

func TestParsePromptClampsRunesWithoutChangingInput(t *testing.T) {
	long := strings.Repeat("界", 210)
	lines := []string{long + "?", "$ " + long, "1. " + long, "2. " + long}
	before := append([]string(nil), lines...)
	got, ok := ParsePrompt(lines)
	if !ok || got.Question != long[:len("界")*200] || got.Summary != got.Question {
		t.Fatalf("ParsePrompt = %+v, %v", got, ok)
	}
	for _, option := range got.Options {
		if option.Label != got.Question {
			t.Errorf("label not clamped to 200 runes: %q", option.Label)
		}
	}
	if !reflect.DeepEqual(lines, before) {
		t.Fatal("ParsePrompt changed its input")
	}
}

package session

import (
	"reflect"
	"testing"
)

func TestResolveEditor(t *testing.T) {
	tests := []struct {
		name      string
		cfg       string
		visualEnv string
		editorEnv string
		wantName  string
		wantArgs  []string
	}{
		{
			name:      "config beats visual and editor",
			cfg:       "nvim",
			visualEnv: "emacs",
			editorEnv: "nano",
			wantName:  "nvim",
			wantArgs:  []string{},
		},
		{
			name:      "visual beats editor when config empty",
			cfg:       "",
			visualEnv: "emacs",
			editorEnv: "nano",
			wantName:  "emacs",
			wantArgs:  []string{},
		},
		{
			name:      "editor used when config and visual empty",
			cfg:       "",
			visualEnv: "",
			editorEnv: "nano",
			wantName:  "nano",
			wantArgs:  []string{},
		},
		{
			name:      "vi fallback when all empty",
			cfg:       "",
			visualEnv: "",
			editorEnv: "",
			wantName:  "vi",
			wantArgs:  []string{},
		},
		{
			name:      "whitespace-only config falls through",
			cfg:       "   ",
			visualEnv: "emacs",
			editorEnv: "",
			wantName:  "emacs",
			wantArgs:  []string{},
		},
		{
			name:      "whitespace-only env vars treated as empty",
			cfg:       "",
			visualEnv: "   ",
			editorEnv: "\t",
			wantName:  "vi",
			wantArgs:  []string{},
		},
		{
			name:      "multi-token config splits into name and args",
			cfg:       "code --wait",
			visualEnv: "",
			editorEnv: "",
			wantName:  "code",
			wantArgs:  []string{"--wait"},
		},
		{
			name:      "multi-token VISUAL splits when config empty",
			cfg:       "",
			visualEnv: "emacs -nw",
			editorEnv: "",
			wantName:  "emacs",
			wantArgs:  []string{"-nw"},
		},
		{
			name:      "quoted editor path stays intact",
			cfg:       `"/Applications/Visual Studio Code.app/Contents/Resources/app/bin/code" --wait`,
			visualEnv: "",
			editorEnv: "",
			wantName:  "/Applications/Visual Studio Code.app/Contents/Resources/app/bin/code",
			wantArgs:  []string{"--wait"},
		},
		{
			name:      "quoted editor arg stays intact",
			cfg:       `code --goto "main.go:12:4"`,
			visualEnv: "",
			editorEnv: "",
			wantName:  "code",
			wantArgs:  []string{"--goto", "main.go:12:4"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("VISUAL", tc.visualEnv)
			t.Setenv("EDITOR", tc.editorEnv)

			name, args, err := resolveEditor(tc.cfg)
			if err != nil {
				t.Fatalf("resolveEditor: unexpected error: %v", err)
			}
			if name != tc.wantName {
				t.Errorf("name = %q, want %q", name, tc.wantName)
			}
			if !reflect.DeepEqual(args, tc.wantArgs) {
				t.Errorf("args = %v, want %v", args, tc.wantArgs)
			}
		})
	}
}

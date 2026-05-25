package daemon

import "testing"

func TestClassifyCodexPromptScreen(t *testing.T) {
	cases := []struct {
		name       string
		screen     string
		wantKind   CodexPromptKind
		wantAction string
		wantIcon   string
		wantOK     bool
	}{
		{
			name: "edit approval",
			screen: `
▌ Would you like to make the following edits?

  src/app.go
  + return nil

› 1. Yes, apply these edits
  2. No, keep editing

Press enter to confirm
`,
			wantKind:   CodexPromptEditApproval,
			wantAction: "elicitation",
			wantIcon:   "question",
			wantOK:     true,
		},
		{
			name: "command approval",
			screen: `
▌ Would you like to run the following command?

  go test ./...

› 1. Yes, run it
  2. No

Press enter to confirm
`,
			wantKind:   CodexPromptCommandApproval,
			wantAction: "permission",
			wantIcon:   "key",
			wantOK:     true,
		},
		{
			name: "request user input",
			screen: `
request_user_input

Pick the deployment target.

› 1. staging
  2. production

enter to submit answer
`,
			wantKind:   CodexPromptRequestUserInput,
			wantAction: "elicitation",
			wantIcon:   "question",
			wantOK:     true,
		},
		{
			name: "unanswered confirmation",
			screen: `
Unanswered confirmation

Codex is waiting for your answer before it continues.

› 1. Continue
  2. Cancel
`,
			wantKind:   CodexPromptUnansweredConfirmation,
			wantAction: "elicitation",
			wantIcon:   "question",
			wantOK:     true,
		},
		{
			name: "permission grant",
			screen: `
Permission grant

Grant permission for Codex to run commands in this workspace?

› 1. Allow for this session
  2. Ask each time

Press enter to confirm
`,
			wantKind:   CodexPromptPermissionGrant,
			wantAction: "permission",
			wantIcon:   "key",
			wantOK:     true,
		},
		{
			name: "non-prompt transcript",
			screen: `
Earlier output:
Would you like to make the following edits?
The model described a request_user_input flow in prose.

Current status: working
`,
			wantOK: false,
		},
		{
			name: "footer without prompt",
			screen: `
Build complete.

Press enter to confirm
`,
			wantOK: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ClassifyCodexPromptScreen(tc.screen)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v; got %+v", ok, tc.wantOK, got)
			}
			if !tc.wantOK {
				return
			}
			if got.Kind != tc.wantKind || got.Action != tc.wantAction || got.IconState != tc.wantIcon {
				t.Fatalf("ClassifyCodexPromptScreen() = %+v, want kind=%q action=%q icon=%q", got, tc.wantKind, tc.wantAction, tc.wantIcon)
			}
		})
	}
}

func TestClassifyCodexPromptScreen_RequiresFooterOrChoiceEvidence(t *testing.T) {
	promptWithoutFooter := `
▌ Would you like to run the following command?

  git status --short
`
	if got, ok := ClassifyCodexPromptScreen(promptWithoutFooter); ok {
		t.Fatalf("ClassifyCodexPromptScreen() = (%+v, true), want no classification", got)
	}
}

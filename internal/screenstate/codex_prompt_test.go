package screenstate

import "testing"

func TestClassifyCodexPrompt(t *testing.T) {
	cases := []struct {
		name     string
		screen   string
		fixture  string
		wantKind CodexPromptKind
		wantOK   bool
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
			wantKind: CodexPromptEditApproval,
			wantOK:   true,
		},
		{
			// The only kind session 2 could capture live: a real codex-cli
			// 0.149.1 command-approval prompt, verbatim. The other four keep
			// their inline screens, which are themselves committed real
			// captures — driving Codex into each of those states on demand
			// costs more than prompt classification (unchanged by this plan)
			// is worth.
			name:     "command approval",
			fixture:  "codex_prompt_command_approval.txt",
			wantKind: CodexPromptCommandApproval,
			wantOK:   true,
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
			wantKind: CodexPromptRequestUserInput,
			wantOK:   true,
		},
		{
			name: "request user input current pane options",
			screen: `
Pick the deployment target.

› Staging (Recommended)
  Production
  None of the above

↑/↓ to navigate questions
ctrl + p / ctrl + n change question
Type your answer (optional)
Add notes
Proceed
`,
			wantKind: CodexPromptRequestUserInput,
			wantOK:   true,
		},
		{
			name: "request user input current pane freeform",
			screen: `
What should Codex optimize for?

Type your answer (optional)
Add notes

⏎ to submit answer
`,
			wantKind: CodexPromptRequestUserInput,
			wantOK:   true,
		},
		{
			name: "request user input numbered plan questions",
			screen: `
Question 1/3 (3 unanswered)
Should the plan cover only the nine formal FR findings, or also remediate the four items listed as residual risks?

› 1. Nine findings (Recommended)  Fix FR-1 through FR-9.
  2. Include residuals            Also address the residual risks.
  3. None of the above

tab to add notes | enter to submit answer | ←/→ to navigate questions | esc to interrupt
`,
			wantKind: CodexPromptRequestUserInput,
			wantOK:   true,
		},
		{
			name: "unanswered confirmation",
			screen: `
Unanswered confirmation

Codex is waiting for your answer before it continues.

› 1. Continue
  2. Cancel
`,
			wantKind: CodexPromptUnansweredConfirmation,
			wantOK:   true,
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
			wantKind: CodexPromptPermissionGrant,
			wantOK:   true,
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
		{
			name: "transcript mentioning request pane labels",
			screen: `
Implementation notes:
Proceed only after all questions are answered.
Add notes to document the decision.
Type your answer in the issue template.
`,
			wantOK: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			screen := tc.screen
			if tc.fixture != "" {
				screen = readScreenFixture(t, tc.fixture)
			}
			got, ok := ClassifyCodexPrompt(screen)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v; got %q", ok, tc.wantOK, got)
			}
			if !tc.wantOK {
				return
			}
			if got != tc.wantKind {
				t.Fatalf("ClassifyCodexPrompt() = %q, want %q", got, tc.wantKind)
			}
		})
	}
}

func TestClassifyCodexPrompt_RequiresFooterOrChoiceEvidence(t *testing.T) {
	promptWithoutFooter := `
▌ Would you like to run the following command?

  git status --short
`
	if got, ok := ClassifyCodexPrompt(promptWithoutFooter); ok {
		t.Fatalf("ClassifyCodexPrompt() = (%q, true), want no classification", got)
	}
}

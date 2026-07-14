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
			wantKind:   CodexPromptRequestUserInput,
			wantAction: "elicitation",
			wantIcon:   "question",
			wantOK:     true,
		},
		{
			name: "request user input current pane freeform",
			screen: `
What should Codex optimize for?

Type your answer (optional)
Add notes

⏎ to submit answer
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

func TestClassifyCodexClearScreen(t *testing.T) {
	cases := []struct {
		name   string
		screen string
		all    string
		want   bool
	}{
		{
			name:   "reported cleared startup UI",
			screen: codexReportedClearedScreen,
			all:    codexReportedClearedScreen,
			want:   true,
		},
		{
			name:   "cleared startup UI with generic summary wording",
			screen: codexClearedScreen,
			all:    codexClearedScreen,
			want:   true,
		},
		{
			name:   "cleared composer without startup box",
			screen: codexClearedComposerOnlyScreen,
			all:    codexClearedComposerOnlyScreen,
			want:   true,
		},
		{
			name:   "visible prompt is not a clear screen",
			screen: codexEditApprovalScreen,
			all:    codexEditApprovalScreen,
			want:   false,
		},
		{
			name:   "nonzero context is not a clear screen",
			screen: codexNonzeroContextScreen,
			all:    codexNonzeroContextScreen,
			want:   false,
		},
		{
			name:   "transcript in scrollback blocks clear",
			screen: codexClearedScreen,
			all:    codexClearedScreenWithTranscript,
			want:   false,
		},
		{
			name:   "prompt marker in transcript blocks clear",
			screen: codexClearedComposerOnlyScreen,
			all:    "› Earlier output: type /clear when done\n\n" + codexClearedComposerOnlyScreen,
			want:   false,
		},
		{
			name:   "assistant marker in transcript blocks clear",
			screen: codexClearedComposerOnlyScreen,
			all:    "• I will inspect the hook flow.\n\n" + codexClearedComposerOnlyScreen,
			want:   false,
		},
		{
			name:   "tool tree marker in transcript blocks clear",
			screen: codexClearedComposerOnlyScreen,
			all:    "└ Read codex_prompt.go\n\n" + codexClearedComposerOnlyScreen,
			want:   false,
		},
		{
			name:   "labeled transcript blocks clear",
			screen: codexClearedComposerOnlyScreen,
			all:    "User: investigate /clear\nCodex: checking\n\n" + codexClearedComposerOnlyScreen,
			want:   false,
		},
		{
			name:   "empty scrollback does not clear",
			screen: codexClearedComposerOnlyScreen,
			all:    "\n\n",
			want:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyCodexClearScreen(tc.screen, tc.all); got != tc.want {
				t.Fatalf("ClassifyCodexClearScreen() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestClassifyCodexCompletedTurnScreen(t *testing.T) {
	cases := []struct {
		name   string
		screen string
		want   bool
	}{
		{
			name:   "completed turn with composer",
			screen: codexCompletedTurnScreen,
			want:   true,
		},
		{
			name:   "active working turn",
			screen: codexWorkingTurnScreen,
			want:   false,
		},
		{
			name:   "prompt screen takes precedence",
			screen: codexCompletedThenPromptScreen,
			want:   false,
		},
		{
			name:   "worked for resumed to working",
			screen: codexWorkedThenWorkingScreen,
			want:   false,
		},
		{
			name:   "worked for without trailing composer",
			screen: codexWorkedForNoComposerScreen,
			want:   false,
		},
		{
			name:   "transcript prose mentioning worked for",
			screen: codexWorkedForProseScreen,
			want:   false,
		},
		{
			name:   "no status line at all",
			screen: codexClearedComposerOnlyScreen,
			want:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyCodexCompletedTurnScreen(tc.screen); got != tc.want {
				t.Fatalf("ClassifyCodexCompletedTurnScreen() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestClassifyCodexInterruptedTurnScreen(t *testing.T) {
	cases := []struct {
		name   string
		screen string
		want   bool
	}{
		{
			name:   "interrupted turn with composer",
			screen: codexInterruptedTurnScreen,
			want:   true,
		},
		{
			name:   "conversation interrupted with composer",
			screen: codexConversationInterruptedScreen,
			want:   true,
		},
		{
			name:   "installed 0.144.3 interruption with dim composer placeholder",
			screen: codexInstalledInterruptedTurnScreen,
			want:   true,
		},
		{
			name:   "interrupted then resumed to working",
			screen: codexInterruptedThenWorkingScreen,
			want:   false,
		},
		{
			name:   "interrupted without trailing composer",
			screen: codexInterruptedNoComposerScreen,
			want:   false,
		},
		{
			name:   "prompt screen takes precedence",
			screen: codexInterruptedThenPromptScreen,
			want:   false,
		},
		{
			name:   "transcript prose mentioning interrupted",
			screen: codexInterruptedProseScreen,
			want:   false,
		},
		{
			name:   "working spinner esc-to-interrupt is not interrupted",
			screen: codexWorkingTurnScreen,
			want:   false,
		},
		{
			name:   "no status line at all",
			screen: codexClearedComposerOnlyScreen,
			want:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyCodexInterruptedTurnScreen(tc.screen); got != tc.want {
				t.Fatalf("ClassifyCodexInterruptedTurnScreen() = %v, want %v", got, tc.want)
			}
		})
	}
}

const codexCompletedTurnScreen = `
• Ran tests
  go test ./internal/daemon

• Worked for 1m 30s

› Use /skills to list available skills

gpt-5.5 xhigh · ~/1_dev_projects/sglavoie_agentmux · Context 12% used
`

const codexWorkingTurnScreen = `
• Worked for 1m 30s

• Working (5s • Esc to interrupt)

›
`

const codexCompletedThenPromptScreen = `
• Worked for 45s

▌ Would you like to make the following edits?

  src/app.go
  + return nil

› 1. Yes, apply these edits
  2. No, keep editing

Press enter to confirm
`

const codexWorkedThenWorkingScreen = `
• Worked for 12s

• Working (3s • Esc to interrupt)

  reading files…
`

const codexWorkedForNoComposerScreen = `
• Worked for 20s

• Ran build
  go build ./...
`

const codexWorkedForProseScreen = `
• The migration worked for the staging cluster last week.
• Ran tests

› Use /skills to list available skills

context 8% · gpt-5-codex · default · Plan mode
`

const codexInterruptedTurnScreen = `
• Ran tests
  go test ./internal/daemon

⏹ Interrupted by user

› Use /skills to list available skills

gpt-5.5 xhigh · ~/1_dev_projects/sglavoie_agentmux · Context 12% used
`

const codexConversationInterruptedScreen = `
• Reading files…

⚠ Conversation interrupted — tell the model what to do differently

›

context 8% · gpt-5-codex · default · Plan mode
`

const codexInstalledInterruptedTurnScreen = `
• I’ll run the command and wait for it to finish.

■ Conversation interrupted - tell the model what to do differently. Something went wrong? Hit /feedback to report the issue.

› Find and fix a bug in @filename

gpt-5.6-sol xhigh · ~/1_dev_projects/sglavoie_agentmux
`

const codexInterruptedThenWorkingScreen = `
⏹ Interrupted by user

• Working (3s • Esc to interrupt)

  reading files…
`

const codexInterruptedNoComposerScreen = `
⏹ Interrupted by user

• Ran build
  go build ./...
`

const codexInterruptedThenPromptScreen = `
⏹ Interrupted by user

▌ Would you like to make the following edits?

  src/app.go
  + return nil

› 1. Yes, apply these edits
  2. No, keep editing

Press enter to confirm
`

const codexInterruptedProseScreen = `
• The deploy was interrupted for the staging cluster last week.
• Ran tests

› Use /skills to list available skills

context 8% · gpt-5-codex · default · Plan mode
`

const codexReportedClearedScreen = `
╭─────────────────────────────────────────────────╮
│ >_ OpenAI Codex (v0.137.0)                      │
│                                                 │
│ model:       gpt-5.5 xhigh   /model to change   │
│ directory:   ~/1_dev_projects/sglavoie_agentmux │
│ permissions: YOLO mode                          │
╰─────────────────────────────────────────────────╯

  Tip: New Use /fast to enable our fastest inference with increased plan usage.

Token usage: total=880,434 input=839,452 (+ 5,946,496 cached) output=40,982 (reasoning 21,671)
To continue this session, run codex resume 019e9855-77d8-7803-9a5a-fb2663a5940d

 
› Use /skills to list available skills
 
  gpt-5.5 xhigh · ~/1_dev_projects/sglavoie_agentmux · Context 0% used
`

const codexClearedScreen = `
╭────────────────────────────────────────────╮
│ >_ OpenAI Codex                            │
│ model: gpt-5-codex                         │
│ directory: /repo                           │
│ permissions: on-request                    │
╰────────────────────────────────────────────╯

Session summary is available with codex resume.
Token accounting changed wording in this build.

› Use /skills to list available skills

context 0% · gpt-5-codex · default · Plan mode
`

const codexClearedComposerOnlyScreen = `
› Use /skills to list available skills

context 0% · gpt-5-codex · default · Plan mode
`

const codexNonzeroContextScreen = `
› Use /skills to list available skills

context 12% · gpt-5-codex · default · Plan mode
`

const codexClearedScreenWithTranscript = `
User: investigate why /clear leaves the stop icon.
Codex: I will inspect the hook flow.

` + codexClearedScreen

package screenstate

// The Codex screen fixtures the classifier tests read.

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

// codexEditApprovalScreen is a copy of the daemon fixture of the same name. The
// daemon keeps its own because four probe tests still drive it through a Kitty
// fake; this one exists so the moved clear-screen test above keeps its
// visible-prompt case.
const codexEditApprovalScreen = `
▌ Would you like to make the following edits?

  src/app.go
  + return nil

› 1. Yes, apply these edits
  2. No, keep editing

Press enter to confirm
`

// The screens below have no live capture behind them, for the reason
// fixture_test.go documents. Each holds a shape that would need a Codex build
// that does not exist yet (a renamed spinner) or a hand-scrolled pane (a
// composer above its marker).

const codexRenamedSpinnerTimerScreen = `
• Worked for 12s

• Cogitating (3s)

› Use /skills to list available skills
`

const codexRenamedSpinnerHintScreen = `
• Worked for 12s

• Cogitating · press ctrl+c to cancel

› Use /skills to list available skills
`

// codexFramedCompletedScreen mirrors the framing in
// testdata/codex_idle_completed_t0.txt at a width the test file can hold. The
// three screens after it are the same bytes with the frame taken away, so
// TestCodexCompletedMarkerRequiresItsFrame fails for the frame and nothing else.

const codexCompletedFrameRule = `────────────────────────────────────────`

const codexFramedCompletedScreen = `
• Ran sleep 8
  └ (no output)

` + codexCompletedFrameRule + `

• Completed.

` + codexCompletedFrameRule + `

› Ask Codex to do anything

  gpt-5.6-sol high · /private/tmp/amx-fixture-capture · Context 2% used
`

const codexUnframedCompletedScreen = `
• Ran sleep 8
  └ (no output)

• Completed.

› Ask Codex to do anything

  gpt-5.6-sol high · /private/tmp/amx-fixture-capture · Context 2% used
`

const codexHalfFramedCompletedScreen = `
• Ran sleep 8
  └ (no output)

` + codexCompletedFrameRule + `

• Completed.

› Ask Codex to do anything

  gpt-5.6-sol high · /private/tmp/amx-fixture-capture · Context 2% used
`

const codexCompletedProseScreen = `
• The first migration completed.

› Ask Codex to do anything

  gpt-5.6-sol high · /private/tmp/amx-fixture-capture · Context 2% used
`

const codexComposerBeforeWorkedForScreen = `
› Use /skills to list available skills

• Worked for 12s
`

const codexComposerBeforeInterruptedScreen = `
› Use /skills to list available skills

⏹ Interrupted by user
`

// codexCompletedWithBackgroundTerminalScreen is codexFramedCompletedScreen with
// Codex's running-background-terminal footer inserted and NOTHING else changed.
// Keeping it a one-line delta is what lets the subtest above attribute the
// refusal to the footer rather than to any other gate.
const codexCompletedWithBackgroundTerminalScreen = `
• Ran sleep 8
  └ (no output)

` + codexCompletedFrameRule + `

• Completed.

` + codexCompletedFrameRule + `

  1 background terminal running · /ps to view · /stop to close

› Ask Codex to do anything

  gpt-5.6-sol high · /private/tmp/amx-fixture-capture · Context 2% used
`

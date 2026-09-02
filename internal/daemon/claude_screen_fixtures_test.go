package daemon

// Claude screen fixtures shared by the daemon's screen-probe tests. They are the
// eight terminal shapes the probes are written against — settled, quiescent,
// working, background-task, permission-prompt, queued-input, context-limit and
// interrupted — and they live here rather than beside the classifier in
// internal/daemon/activity because every consumer is a Handler-level probe test.

const claudeIdleScreen = `✻ Cooked for 17m 6s

╭──────────────────────────────────────────────────╮
│ ❯                                                  │
╰──────────────────────────────────────────────────╯
  ? for shortcuts                    (shift+tab to cycle)
`

// claudeQuiescentIdleScreen is the weakest terminal shape Claude produces: a
// bare empty composer with no completion line and no interruption marker at
// all. Claude shows exactly this after /clear, after /compact, at session
// start, and after a turn whose completion line has scrolled away, which is why
// screenstate.TierQuiescent settles at all — and why it is the tier that pays
// the most stabilization samples.
const claudeQuiescentIdleScreen = `╭──────────────────────────────────────────────────╮
│ ❯                                                  │
╰──────────────────────────────────────────────────╯
  ? for shortcuts                    (shift+tab to cycle)
`

const claudeWorkingScreen = `✳ Thinking… (12s · ↑ 2.1k tokens · esc to interrupt)

╭──────────────────────────────────────────────────╮
│ ❯                                                  │
╰──────────────────────────────────────────────────╯
`

const claudeBackgroundTaskScreen = `Running go test in the background (ctrl+b to manage background tasks)

╭──────────────────────────────────────────────────╮
│ ❯                                                  │
╰──────────────────────────────────────────────────╯
`

const claudePermissionScreen = `Do you want to make this edit to app.go?

❯ 1. Yes
  2. No, and tell Claude what to do differently

╭──────────────────────────────────────────────────╮
│ ❯                                                  │
╰──────────────────────────────────────────────────╯
`

const claudeQueuedInputScreen = `✻ Cooked for 2m 10s

╭──────────────────────────────────────────────────╮
│ ❯ and now write the tests                          │
╰──────────────────────────────────────────────────╯
  ? for shortcuts
`

const claudeContextLimitStopScreen = `Context limit reached · /compact or /clear to continue

╭──────────────────────────────────────────────────╮
│ ❯                                                  │
╰──────────────────────────────────────────────────╯
`

const claudeInterruptedScreen = `⎿ Interrupted by user

╭──────────────────────────────────────────────────╮
│ ❯                                                  │
╰──────────────────────────────────────────────────╯
  ? for shortcuts
`

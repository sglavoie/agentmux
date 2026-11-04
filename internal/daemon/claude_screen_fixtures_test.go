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

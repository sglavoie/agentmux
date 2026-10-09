package screenstate

import (
	"strconv"
	"strings"
)

// State is what a pane's screen text says about the harness turn. It is a
// reading of the screen and nothing more: see the package doc for why no value
// here implies an icon.
//
// Unknown is the zero value on purpose. Every construction path that fails to
// reach a positive conclusion — an unrecognized harness, a screen with no
// composer at all, a classifier that bailed early — therefore yields Unknown,
// and Unknown never settles anything. Fail-closed is the default because the
// bug this package exists to close was exactly the opposite default: absence of
// scary evidence read as evidence of a finished turn.
type State uint8

const (
	// Unknown means the screen did not support any conclusion. It is never
	// terminal evidence.
	Unknown State = iota
	// Working means positive evidence of an in-flight turn was seen.
	Working
	// Blocked means the pane is waiting on the user — a permission or choice
	// prompt, or a context-limit recovery marker.
	Blocked
	// Terminal means no veto fired and the turn looks finished.
	Terminal
)

// String renders a State for diagnostics and test failure messages.
func (s State) String() string {
	switch s {
	case Working:
		return "working"
	case Blocked:
		return "blocked"
	case Terminal:
		return "terminal"
	default:
		return "unknown"
	}
}

// Tier grades HOW a Terminal screen proved itself, from weakest to strongest.
// It exists so the probe can spend more stabilization samples on weaker
// evidence; it is not a second State and it never overrides one.
//
// The ordering is the contract: a caller may compare tiers, but must not assume
// any particular sample count, which lives with the probe.
type Tier uint8

const (
	// TierNone is the tier of every non-Terminal observation.
	TierNone Tier = iota
	// TierQuiescent is a safe screen with an empty composer and no positive
	// terminal marker at all. It is the weakest terminal evidence and the most
	// common: a Claude pane after /clear, after /compact, at session start, or
	// after a turn whose completion line has scrolled away shows exactly this
	// and nothing more. Requiring a marker would strand those panes busy
	// forever, which is why quiescence stays a tier rather than being rejected.
	TierQuiescent
	// TierTerminalShape is a finished-line shape — "<word> for <duration>" at
	// end of line. Stronger than quiescence, weaker than an explicit marker,
	// because only the shape is pinned; the harness picks the word.
	TierTerminalShape
	// TierTerminalExplicit is an explicit finished/interrupted marker the
	// harness generated, naming why the turn ended.
	TierTerminalExplicit
)

// String renders a Tier for diagnostics and test failure messages.
func (t Tier) String() string {
	switch t {
	case TierQuiescent:
		return "quiescent"
	case TierTerminalShape:
		return "terminal_shape"
	case TierTerminalExplicit:
		return "terminal_explicit"
	default:
		return "none"
	}
}

// Veto is a bitset of reasons a screen must not be settled to a synthetic stop.
//
// Vetoes are computed FIRST and UNCONDITIONALLY, before any positive terminal
// evidence is consulted. That ordering is load-bearing rather than stylistic: a
// live Claude working line reads `● Reticulating… (4m 29s · ↓ 17.2k tokens ·
// thought for 5s)`, which contains a past-tense "for 5s" fragment. A classifier
// that looked for finished evidence first would settle a pane that is visibly
// working. Adapters must keep this order.
type Veto uint16

const (
	// VetoInterruptHint is a generic "<key> to interrupt/cancel" hint. Present
	// on some harnesses and some turn shapes only. Its ABSENCE proves nothing —
	// Claude Code 2.1.x stopped rendering it, which is the whole bug — so it is
	// a one-way veto and never positive terminal evidence.
	VetoInterruptHint Veto = 1 << iota
	// VetoLiveStatusLine is a progress ellipsis plus a parenthesized live
	// elapsed timer on the same line. This is a UI convention rather than a
	// wording choice, which makes it the most durable wording-based veto
	// available.
	VetoLiveStatusLine
	// VetoBackgroundTask is a detached background-task or running-agent manager
	// hint ("ctrl+b to …"). A pane whose main composer looks idle can still own
	// running work.
	VetoBackgroundTask
	// VetoBlockingPrompt is a permission, elicitation, or choice prompt waiting
	// on the user.
	VetoBlockingPrompt
	// VetoContextLimit is the context-limit recovery marker. It is what lets the
	// dedicated context-limit probe win a race against idle evidence: a
	// context-limited screen never observes as terminal.
	VetoContextLimit
	// VetoComposerBusy is queued input in the composer, or no composer on screen
	// at all. Both mean the same thing here — the pane is not sitting at a ready
	// prompt — and neither is safe to settle.
	VetoComposerBusy
)

// vetoNames pairs each Veto bit with the name String reports for it, in bit
// order so a set always renders deterministically.
var vetoNames = []struct {
	bit  Veto
	name string
}{
	{VetoInterruptHint, "interrupt_hint"},
	{VetoLiveStatusLine, "live_status_line"},
	{VetoBackgroundTask, "background_task"},
	{VetoBlockingPrompt, "blocking_prompt"},
	{VetoContextLimit, "context_limit"},
	{VetoComposerBusy, "composer_busy"},
}

// String renders a veto SET as a pipe-joined list of names, or "none" when
// empty. The daemon logs a declined observation's vetoes verbatim, and a bare
// bitset number there is unreadable at the exact moment someone is asking why a
// tab is stuck on ⚡ — naming every bit is the whole point of keeping vetoes as
// a set rather than a first hit. An unknown bit (a veto added without a name
// here) renders as its hex value rather than being dropped.
func (v Veto) String() string {
	if v == 0 {
		return "none"
	}
	var names []string
	rest := v
	for _, known := range vetoNames {
		if v&known.bit != 0 {
			names = append(names, known.name)
			rest &^= known.bit
		}
	}
	if rest != 0 {
		names = append(names, "0x"+strconv.FormatUint(uint64(rest), 16))
	}
	return strings.Join(names, "|")
}

// maxStatusLineRunes bounds Observation.StatusLine. A pane line is unbounded in
// principle (a wide monitor plus a wrapped capture), and this string exists only
// so a human reading a diagnostic can see which line the adapter keyed on.
const maxStatusLineRunes = 120

// Observation is everything an adapter concluded about one screen capture.
//
// It is data, not a decision. Callers hand it to the daemon reducer, which is
// the only arbiter of what a pane's icon becomes.
type Observation struct {
	// State is the reading of the screen; Unknown when nothing was concluded.
	State State
	// Tier grades Terminal evidence and is TierNone otherwise.
	Tier Tier
	// Vetoes is every reason found not to settle, kept as a set rather than a
	// first-hit so diagnostics can name all of them.
	Vetoes Veto
	// Fingerprint is the normalized-screen hash from Fingerprint, ready to use
	// as an AuthoritativeIdle generation. It is never zero.
	Fingerprint int64
	// StatusLine is the harness status line the adapter keyed on, bounded to
	// maxStatusLineRunes. Diagnostics only — never parse it.
	StatusLine string
}

// SettleableTerminal reports whether this observation is safe to act on as a
// finished turn. It is deliberately the conjunction of both halves rather than
// a State test alone: an adapter that ever sets Terminal alongside a veto has a
// bug, and this reads as the invariant instead of trusting it.
func (o Observation) SettleableTerminal() bool {
	return o.State == Terminal && o.Vetoes == 0
}

// Adapter classifies one harness's screen text. Adapters own UI-copy knowledge;
// everything in this package outside an adapter is harness-agnostic.
type Adapter interface {
	// Harness is the canonical lowercase harness name, matching
	// harness.Impl.Name().
	Harness() string
	// Classify reads a plain-text capture of a pane's visible screen, one line
	// per terminal row: `kitten @ get-text --extent=screen` on Kitty,
	// `capture-pane -p` on tmux. Text captured with styling goes through
	// StripANSI first. It must compute vetoes first and unconditionally, and
	// must consult positive terminal evidence only on a veto-free screen.
	Classify(screen string) Observation
}

// adapters is keyed by canonical lowercase harness name.
//
// Registration is not the same as being on a probe path. The Copilot adapter is
// here because For() is the registry every caller asks, and an adapter that
// exists but cannot be looked up is a worse fiction than one that does not
// exist; no screenIdleSpec names copilot today, so nothing in internal/daemon
// classifies a Copilot screen yet. copilot.go's composition decision says which
// direction that wiring may eventually go.
var adapters = map[string]Adapter{
	claudeAdapter{}.Harness():  claudeAdapter{},
	codexAdapter{}.Harness():   codexAdapter{},
	copilotAdapter{}.Harness(): copilotAdapter{},
}

// For returns the adapter for a harness name, reporting whether one exists.
//
// Unlike harness.Lookup it does NOT fall back to Claude for an unknown name:
// classifying one harness's pane with another's UI copy would produce confident
// nonsense, and the fail-closed answer for "no adapter" is to observe nothing.
// Registering Copilot narrows that fence rather than removing it — an
// unrecognized name still gets no adapter, which is the load-bearing half.
func For(harnessName string) (Adapter, bool) {
	adapter, ok := adapters[strings.ToLower(strings.TrimSpace(harnessName))]
	return adapter, ok
}

// boundStatusLine trims a status line and clamps it to maxStatusLineRunes.
func boundStatusLine(line string) string {
	trimmed := strings.TrimSpace(line)
	runes := []rune(trimmed)
	if len(runes) <= maxStatusLineRunes {
		return trimmed
	}
	return string(runes[:maxStatusLineRunes])
}

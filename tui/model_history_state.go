package tui

import (
	"github.com/sglavoie/agentmux/internal/transcript"
)

// modelHistoryState holds the conversation browser and viewer state.
// Embedded by value so Bubble Tea updates keep the model copy semantics.
type modelHistoryState struct {
	// transcript index + per-harness readers are loaded from the filesystem
	// cache at startup and reused across every ModeSessionSearch refresh/search.
	transcriptIndex   *transcript.Index
	transcriptReaders map[string]transcript.Reader

	// Background warm-up state for ModeSessionSearch. The startup cache load
	// publishes warm rows before the slower source refresh and retention prune.
	sessionCacheLoadInFlight bool
	sessionRefreshInFlight   bool

	// session search view (ModeSessionSearch). sessionResults holds the
	// most recent transcript.Search output. sessionQuery is the free-text
	// query typed after `/`. sessionSearchMode is true while the user is
	// actively editing sessionQuery (mirrors pickerSearchMode). The chip
	// fields hold the current harness/profile/project filters. sessionLoading
	// covers cache/query result loading; sessionRefreshInFlight tracks the
	// slower background scan so warmed rows can remain visible while it runs.
	sessionQuery   string
	sessionResults []transcript.Result
	sessionCursor  int
	sessionOffset  int
	// sessionWantedKey is the transcript.IndexKey of the conversation a
	// history view was opened on. Results select it the first time they list
	// it, or until the operator moves; then it is spent.
	sessionWantedKey string
	// Exact conversation scope for the native browser's specialized actions.
	historyConversation string
	// historyOnly marks a model started in Past Sessions with no session
	// list behind it (NewHistoryModel).
	historyOnly         bool
	sessionSearchMode   bool
	sessionLoading      bool
	sessionRefreshErr   error
	sessionSearchErr    error
	sessionSearchSeq    int      // bumps on each Search dispatch; stale msgs are dropped
	filterHarness       []string // chip filter; "" means "no filter"
	filterProfile       []string
	filterProject       []string
	filterMessageSource string // "" (any), "user", or "harness"

	// chip picker overlay (ModeChipPicker) opened from ModeSessionSearch or
	// ModePlanSearch to choose a categorical filter. chipPickerKind selects
	// which filter is being edited, while chipPickerReturnMode preserves the
	// owning search view. chipPickerAllOptions is the full
	// list captured at open time; chipPickerOptions is the visible list used by
	// the cursor, renderer, and selection (the leading empty string represents
	// "(any)" when visible). chipPickerCursor/Offset follow the same window
	// contract as sessionCursor/sessionOffset.
	chipPickerKind       chipPickerKind
	chipPickerAllOptions []string
	chipPickerOptions    []string
	chipPickerCursor     int
	chipPickerOffset     int
	chipPickerQuery      string
	chipPickerSearchMode bool
	chipPickerReturnMode Mode

	// session viewer (ModeSessionView). viewerSession is the metadata of the
	// transcript currently being read; nil when the viewer is closed.
	// viewerMessages is the parsed message list. The viewer splits selection
	// from windowing: viewerCursor is the selected message row (always a
	// valid index into viewerMessages when the viewer is open), and
	// viewerScroll is the top-of-window index. j/k/g/G/pgup/pgdn move the
	// cursor; scroll follows via scrollViewerToCursor. viewerMatchLines
	// holds the indices into viewerMessages whose text contains the current
	// sessionQuery (lowercased substring match); viewerMatchIdx is the
	// cursor into viewerMatchLines for n/N navigation (which moves
	// viewerCursor to the chosen match). viewerLoading covers the async
	// ReadMessages call; viewerLoadSeq is a monotonic counter separate from
	// sessionSearchSeq so a stale viewer load arriving after the user moved
	// on is dropped.
	viewerSession    *transcript.SessionMeta
	viewerMessages   []transcript.Message
	viewerCursor     int
	viewerScroll     int
	viewerMatchIdx   int
	viewerMatchLines []int
	viewerLoading    bool
	viewerLoadErr    error
	viewerLoadSeq    int

	// delete flow (ModeDeleteChoice). deleteTarget holds the entry the user
	// selected with `d` from ModeSessionSearch; nil when no delete is in
	// progress. The prompt itself reads from this pointer.
	deleteTarget *transcript.SessionMeta

	// ModeMessageDetail overlay state. messageDetailIdx is the message
	// index captured at overlay open time; -1 means closed.
	// messageDetailScroll is the top wrapped-line index.
	messageDetailIdx    int
	messageDetailScroll int
}

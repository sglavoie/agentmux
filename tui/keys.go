package tui

// Key string constants used in KeyPressMsg switch cases.
//
// A constant here names a keystroke, not a surface. After the dashboard keymap
// shrank to the entries in dashboardBindings() plus the cursor and
// structure keys handleListKey owns, most of the session mnemonics below —
// keyE, keyR, keyC, keyDashboardLaunch, keyClearStatus, keyMarkActive, keyClose, keyO,
// keyOpenEditor, keyY, keyLock, keyM — are pressed inside the actions overlay
// (`a`) rather than at the list. The capability each comment describes is
// unchanged; only the surface in front of it moved. Where a spelling means
// different things in different modes, the comment says which mode.
const (
	keyUp              = "up"
	keyDown            = "down"
	keyLeft            = "left"
	keyRight           = "right"
	keyJ               = "j"
	keyK               = "k"
	keyG               = "g"
	keyGG              = "G"
	keySlash           = "/"
	keyAllSessions     = "S" // dashboard: all-project Past Sessions, newest activity first
	keyColon           = ":" // opens ModeCommandPalette from ModeList only
	keyF               = "f"
	keyWorktreeFork    = "w" // Past Sessions search/viewer: confirm an isolated Git worktree fork
	keyCopySessionID   = "i" // Past Sessions search/viewer: copy the transcript's canonical session ID
	keyA               = "a"
	keyO               = "o"
	keyL               = "l"
	keyY               = "y"
	keyCopyPath        = "Y"
	keyN               = "n" // ModeConfirm: decline the prompt
	keySpace           = "space"
	keyEnter           = "enter"
	keyEsc             = "esc"
	keyQ               = "q"
	keyCtrlC           = "ctrl+c"
	keyE               = "e"
	keyClose           = "X" // closes the highlighted session
	keyR               = "r"
	keyBackspace       = "backspace"
	keyQuestion        = "?" // opens ModeHelp overlay (dashboard, Past Sessions, Plans)
	keyC               = "c" // opens ModeConfigPicker for profile override
	keyDashboardLaunch = "C" // opens ModeConfigPicker for dashboard launch
	keyClearStatus     = "x" // clears the highlighted session's icon
	keyMarkActive      = "b" // marks the highlighted session active (⚡)
	keyOpenEditor      = "O" // opens the project directory in the configured editor
	keyLock            = "L" // toggle the session lock (blocks close/restart/override)
	keyM               = "m" // toggle session mute (suppresses notifications)
	// keyD is no longer a dashboard key: the deprecation banner it dismissed is
	// gone and the detail overlay it opened is reached through the inspector and
	// the palette. What is left are four overlay-local meanings, each in a mode
	// with its own handler — close the detail overlay, delete a past session,
	// remove a worktree, and dismiss a health notice.
	keyD = "d"
	// keyASLRuns is now a ModeASLRuns key alone: it closes the view it used to
	// open. Plans, Worktrees, and ASL Runs are palette entries on the dashboard.
	// Past Sessions keeps its direct `S` shortcut for browsing recent activity.
	keyASLRuns              = "A"
	keyASLWorkbench         = "w" // ModeASLRuns: open the run's Workbench deep link
	keyASLRespond           = "r" // ModeASLRuns: open the response/resume form for the run
	keyASLStop              = "s" // ModeASLRuns: confirm stop after the current session
	keyASLAbort             = "a" // ModeASLRuns: confirm aborting the in-flight turn
	keyDeleteWorktreeBranch = "D" // ModeWorktrees: remove checkout and safely delete merged branch
	keyForgetWorktree       = "F" // ModeWorktrees: forget missing or invalid sidecar metadata
	keyPinProject           = "P" // ModePicker: pin/unpin the highlighted configured project
	keySortPlan             = "o" // ModePlanSearch: cycles the result-sort mode
	keyPlanOpenEditor       = "e" // ModePlanSearch/ModePlanView: open the plan directory in the editor
	keyToggleTimeline       = "t" // toggle flat list ↔ timeline in ModeDetail
	keyToggleTimestamps     = "T" // toggle timestamp column in timeline (ModeDetail)
	// keyPhasePrev/keyPhaseNext belong to the ModeDetail timeline alone. The
	// dashboard used to spell a jump chord with the same two glyphs — `]`/`[` to
	// arm it, `]]`/`[[` and `}`/`{` to jump — and that whole vocabulary is
	// retired: `n`/`N` walks the Needs you queue instead, and neither bracket nor
	// brace answers anything in ModeList.
	keyPhasePrev = "[" // jump to previous phase header in timeline
	keyPhaseNext = "]" // jump to next phase header in timeline

	// keyUndoClose reopens the session most recently closed, for the five
	// seconds that close stays recoverable. It is a dashboard key rather than a
	// palette entry alone because what it undoes is measured in seconds: a
	// recovery reached by opening the palette and typing a command name is one
	// the operator has already run out of time for. The palette command remains
	// for anyone who reaches for it by name.
	keyUndoClose = "u"

	keyPgUp   = "pgup"   // move up by one page in scrollable views
	keyPgDown = "pgdown" // move down by one page in scrollable views
	keyHome   = "home"   // jump to the top of a scrollable pane
	keyEnd    = "end"    // jump to the bottom of a scrollable pane, resuming a held viewport
	keyCtrlD  = "ctrl+d" // move down by half a page in scrollable views
	keyCtrlU  = "ctrl+u" // move up by half a page in scrollable views
	keyCtrlF  = "ctrl+f" // move down by one page in scrollable views
	keyCtrlB  = "ctrl+b" // move up by one page in scrollable views
	keyCtrlJ  = "ctrl+j" // move down in searchable command menus
	keyCtrlK  = "ctrl+k" // move up in searchable command menus
	keyCtrlN  = "ctrl+n" // move down in searchable command menus
	keyCtrlP  = "ctrl+p" // move up in searchable command menus
	keyCtrlS  = "ctrl+s" // submit a multi-line editor whose Enter inserts a newline

	// keyTogglePreview belongs to the launcher and the plan browser alone. The
	// dashboard retired it with the preview pane it named: `p` there is reserved
	// for the project switcher, and the inspector that replaced the preview has
	// its own key below.
	keyTogglePreview = "p" // launcher/plans: toggles the split-pane live preview
	// keyInspector is the same key string as keyCopySessionID; disambiguation is
	// by mode. In ModeList: show or hide the session inspector.
	keyInspector = "i"
	// `v` toggled the list between Urgency and Projects and is retired. The
	// arrangement is not something an operator changes often enough to spend a
	// letter on: Toggle List View is a palette command, and the view name in the
	// header is a click target, so both a keyboard and a mouse route survive.
	//
	// keyProjectSwitcher opens the dashboard's project-scope overlay. It is the
	// same key the launcher and the plans browser spell as keyTogglePreview, and
	// the dashboard is free to claim it because the preview pane that owned it
	// here is gone.
	keyProjectSwitcher = "p"

	// keyNeedsYouNext/keyNeedsYouPrev walk the Needs you queue in ModeList. The
	// lowercase spelling is the same key string as keyN, which ModeConfirm reads
	// as "no"; disambiguation is by mode, as it is for keyInspector above.
	//
	// They move the cursor and nothing else. Revealing a folded family is part of
	// moving — a row with no line of its own cannot be selected until its family
	// is unfolded — but no terminal is focused and no status icon is
	// acknowledged, which is what separates this from the `}`/`{` jump it
	// replaced.
	keyNeedsYouNext = "n"
	keyNeedsYouPrev = "N"

	// The ModeList arrow-key folding vocabulary — Left to fold a family or step
	// up to its summary, Right to unfold — is retired. Enter is the one fold key
	// now: the summary line it acts on is the only line where Enter has nothing
	// to focus, so there is no ambiguity to resolve with a second pair of keys,
	// and the footer's `enter fold/unfold` gloss is one key rather than three.
	//
	// keyTab/keyShiftTab switch the dashboard between its two body panes. They
	// used to cycle the filter-chip ring as well; the ring is retired, so on a
	// dashboard with no inspector on screen there is nothing to switch to and
	// both keys answer nothing.
	keyTab      = "tab"
	keyShiftTab = "shift+tab"
)

// keyProjectSessionSearch opens the selected project's history from the
// dashboard or launcher.
const keyProjectSessionSearch = "s" // opens ModeSessionSearch filtered to the selected project

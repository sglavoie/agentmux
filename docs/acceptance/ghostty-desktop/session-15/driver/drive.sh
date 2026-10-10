#!/bin/bash
# Drives the Agent Mux.app workspace on the MacBook and records what was
# observed. Runs on the Studio; every key, pointer move, and picture is the
# MacBook's (see README.md).
# usage: drive.sh OUTDIR CASE...
#   cases: fresh claude codex copilot history link ime priority reattach
here=$(cd "$(dirname "$0")" && pwd)
. "$here/env.sh"
OUT=$1; shift
mkdir -p "$OUT"
LOG=$OUT/interaction-acceptance.log; SHOTS=$OUT
. "$here/lib.sh"
vnc_up || { echo "no VNC connection to the MacBook"; exit 1; }

# Sidebar rows, in points: where a click lands on each session's row.
rowy() { case $1 in dashboard) echo 125;; claude) echo 237;; codex) echo 312;; copilot) echo 387;; history) echo 463;; esac; }
# show PROJECT: a real click on its sidebar row; waits for its terminal.
show() { click 100 "$(rowy "$1")"; waitfor 10 attached "$1"; sleep 0.8; }
shown() { since_has "$1"; }
# The process that is the pane on the MacBook, and the harness on the Studio.
panepid() { rq "pfmt $1 '#{pane_pid}'"; }
harnesspid() { sed -n 's/^harness pid //p' "$L/logs/serve-$1.log" | tail -1; }
alive() { kill -0 "$1" 2>/dev/null; }
# Each harness's own keys, as its help lists them.
newline_key() { case $1 in codex) echo ctrl+j;; *) echo shift+Return;; esac; }
clearline() { local i; for i in 1 2 3 4 5; do vnc key ctrl+e ctrl+u BackSpace; done; sleep 0.5; }
PROMPT='Print the integers 1 through 150, one per line, with the word S15MARK before each. No tools, no commentary.'

case_fresh() {
  note "the app copy starts on the MacBook with its workspace open; nothing is shown or driven on the Studio"
  rq "app_quit; obs kill-server 2>/dev/null; app_defaults '$FRAME'; app_start" || say "FAIL: the app did not start"
  sleep 2
  facts "machines" sh -c "echo \"driver: \$(scutil --get ComputerName)\"; printf '. %s/remote-env.sh\n. %s/remote-lib.sh\necho \"app, tmux, daemon, input, pictures: \$(scutil --get ComputerName) (app pid \$(apppid))\"\n' $R $R | $SSH $H /bin/sh -s"
  check "the app subscribed to the world's daemon" rq 'applog | grep -q "feed: live"'
  check "the workspace opened on the Dashboard" rq 'applog | grep -q "workspace: started view dashboard"'
  check "the daemon lists four tmux sessions and no Kitty" ev "rq snapshot | grep -c 'runtime=tmux' | grep -qx 4 && rq snapshot | grep -q 'kitty.ready=False'"
  check "no terminal is attached to any agent yet" ev "[ -z \"\$(rq clients)\" ]"
  for h in claude codex copilot; do
    check "$h runs headless on the Studio (pid $(harnesspid $h)) behind the pane process on the MacBook (pid $(panepid $h))" alive "$(harnesspid $h)"
  done
  facts "daemon snapshot" rq snapshot
  facts "panes" rq 'for p in claude codex copilot history; do echo "$p: $(pstate $p)"; done'
  shot workspace-start
}

# matrix HARNESS: the keyboard and interaction matrix for one real harness.
matrix() {
  local h=$1 nl; nl=$(newline_key $h)
  note "$h: interaction matrix through the embedded terminal"
  local hp pp; hp=$(harnesspid $h); pp=$(panepid $h)
  mark; show $h
  check "$h: a click on its row attached exactly one terminal to its pane" attached $h
  check "$h: the app logged the focus" shown "workspace: focus embedded"
  check "$h: the pane's grid is the viewer's ($(pfmt $h '#{window_width}x#{window_height}'), not the 80x24 it was launched with)" ev "[ \"\$(pfmt $h '#{window_width}')\" -gt 90 ]"
  # The harness's own first-run prompts are answered with real keys.
  if shas $h "trust this folder"; then key Down; key Return; sleep 5; say "$h: answered its folder-trust prompt with Down and Return"; fi
  if shas $h "use my browser"; then key Return; sleep 4; say "$h: kept its browser tools off with Return"; fi
  if shas $h "Confirm folder trust"; then key Return; sleep 5; say "$h: answered its folder-trust prompt with Return"; fi
  check "$h: its composer is on screen" idle $h
  sshot $h "ready"

  typetext "alpha beta"
  check "$h: typed text reaches the composer" swait $h "alpha beta" 8
  key ctrl+a; typetext "Y"
  check "$h: Control+A moved to the start of the line (Y typed there)" swait $h "Yalpha beta" 8
  key ctrl+e; typetext "Z"
  check "$h: Control+E moved to the end (Z typed there)" swait $h "Yalpha betaZ" 8
  key "$nl"; typetext "second"
  sleep 3
  check "$h: $nl made a new line: the two lines sit one above the other in the composer" adjacent $h "Yalpha betaZ" "second"
  check "$h: and nothing was submitted" idle $h
  sshot $h "after typing, Control+A, Control+E, and $nl"
  clearline $h
  check "$h: the composer was cleared with its editing keys" ev "! shas $h 'Yalpha' && ! shas $h second"

  setclip "S15 paste one
S15 paste two"
  key cmd+v; sleep 1
  check "$h: Command+V pasted both lines" ev "shas $h 'S15 paste one' && shas $h 'S15 paste two' || shas $h 'Pasted'"
  check "$h: the pasted line break submitted nothing" idle $h
  sshot $h "after Command+V of two lines"
  clearline $h

  typetext "$PROMPT"; key Return
  check "$h: Return submitted the prompt and the answer reached line 150" swait $h "S15MARK 150" 120
  waitfor 40 idle $h; sleep 2
  check "$h: the same harness process produced it (Studio pid $hp)" alive "$hp"
  say "$h: pane state $(pstate $h)"

  # Scrollback: the harness is full screen, so the history is its own
  # transcript, held by a process the tmux pane keeps; tmux's history is empty.
  check "$h: it draws its own full screen with the mouse on (tmux holds no history for it)" ev "pstate $h | grep -q 'alternate=1 mouse=1'"
  local p; p=$(cellpoint $h 40 12)
  wheel $p 12; sleep 0.6
  check "$h: a real wheel turn scrolled its transcript back (line 150 left the screen)" notline $h "S15MARK 150"
  check "$h: the wheel went to the harness, not into tmux's copy mode" live $h
  sshot $h "after wheel up"
  local w=0; while [ $w -lt 8 ] && ! hasline $h "S15MARK 150"; do wheel $p -12; w=$((w+1)); done
  check "$h: turning the wheel back down returned to the last line" hasline $h "S15MARK 150"
  vnc key shift+Page_Up shift+Page_Up shift+Page_Up shift+Page_Up shift+Page_Up shift+Page_Up shift+Page_Up shift+Page_Up; sleep 1.2
  check "$h: Shift+Page Up (View > Page Up) paged its transcript to the first lines" ev "hasline $h 'S15MARK 3' || hasline $h 'S15MARK 2'"
  check "$h: paging did not enter tmux's copy mode" live $h
  sshot $h "after Shift+Page Up to the top"
  local i=0; while [ $i -lt 14 ] && ! hasline $h "S15MARK 150"; do vnc key shift+Page_Down; sleep 0.4; i=$((i+1)); done
  check "$h: Shift+Page Down (View > Page Down) returned to the last line ($i presses)" hasline $h "S15MARK 150"

  # Selection and copy: Shift takes the drag from the harness.
  setclip "SENTINEL"
  local a b; a=$(textpoint $h "S15MARK 149" 0 -2); b=$(textpoint $h "S15MARK 149" 12)
  drag $a $b shift; sleep 0.5; key cmd+c; sleep 0.6
  local got; got=$(clip)
  say "$h: clipboard after Shift+drag over a line and Command+C: [$got]"
  check "$h: Shift+drag selected the line and Command+C copied exactly it" test "$got" = "S15MARK 149"
  shot $h-selection

  # Find: what tmux holds for a full-screen harness is the visible screen.
  key cmd+f; sleep 0.8
  check "$h: Command+F (Edit > Find) opened tmux's find over the screen" inhistory $h
  typetext "S15MARK 150"; key Return; sleep 0.8
  check "$h: find landed on the typed text" ev "pfmt $h '#{copy_cursor_line}' | grep -q 'S15MARK 150'"
  key Escape; sleep 0.6
  check "$h: Escape returned to the live screen" live $h

  # A plain click and a right click belong to the harness; neither ends it.
  click $p; rclick $p; sleep 0.6
  check "$h: a click and a right click left the same pane process running (MacBook pid $pp)" test "$(panepid $h)" = "$pp"
  check "$h: and the same harness (Studio pid $hp)" alive "$hp"
  key Escape; sleep 0.3
  clearline $h
  shot $h-matrix
}
case_claude() { matrix claude; }
case_codex() { matrix codex; }
case_copilot() { matrix copilot; }

case_history() {
  note "history held by tmux: a program on the main screen whose 3000 lines scrolled away"
  local pp; pp=$(panepid history)
  mark; show history
  check "its terminal is attached" attached history
  local size; size=$(pfmt history '#{history_size}')
  check "tmux holds its history ($size lines) and it does not use the mouse" ev "pstate history | grep -q 'alternate=0 mouse=0' && [ $size -gt 2900 ]"
  key shift+Page_Up; sleep 0.6
  check "Shift+Page Up entered the history" inhistory history
  local pos; pos=$(pfmt history '#{scroll_position}')
  key shift+Page_Up; sleep 0.6
  check "a second Shift+Page Up went a page further (from $pos to $(pfmt history '#{scroll_position}'))" test "$(pfmt history '#{scroll_position}')" -gt "$pos"
  key shift+Page_Down; key shift+Page_Down; sleep 0.6
  check "Shift+Page Down back to the bottom returned to the live screen" live history
  key cmd+Home; sleep 0.6
  check "Command+Home reached the oldest line (position $(pfmt history '#{scroll_position}') of $size)" test "$(pfmt history '#{scroll_position}')" = "$size"
  check "the view is on the first line: [$(pfmt history '#{copy_cursor_line}')]" test "$(pfmt history '#{copy_cursor_line}')" = "LINE 0001 needle-0001"
  shot history-top
  key cmd+End; sleep 0.6
  check "Command+End returned to the live screen" live history
  key cmd+f; sleep 0.8
  check "Command+F opened find" inhistory history
  typetext "needle-0042"; key Return; sleep 0.8
  check "find landed on line 42 of $size: [$(pfmt history '#{copy_cursor_line}')]" test "$(pfmt history '#{copy_cursor_line}')" = "LINE 0042 needle-0042"
  shot history-find
  key cmd+f; sleep 0.6; typetext "needle-00"; key Return; sleep 0.8
  local first; first=$(pfmt history '#{copy_cursor_line}')
  key cmd+g; sleep 0.6
  local next; next=$(pfmt history '#{copy_cursor_line}')
  check "Command+G stepped to the match before ($first then $next)" test "$next" != "$first"
  key cmd+shift+g; sleep 0.6
  check "Shift+Command+G stepped back" test "$(pfmt history '#{copy_cursor_line}')" = "$first"
  key Escape; sleep 0.6
  check "Escape returned to the live screen" live history
  local p; p=$(cellpoint history 40 12)
  wheel $p 6
  check "a real wheel turn scrolled into the history (position $(pfmt history '#{scroll_position}'))" inhistory history
  wheel $p -40
  check "turning it back to the bottom returned to the live screen" live history
  # A plain drag: tmux selects and copies on release.
  setclip "SENTINEL"
  local a b; a=$(textpoint history "LINE 2988 needle-2988" 0 -2); b=$(textpoint history "LINE 2988 needle-2988" 21 2)
  drag $a $b; sleep 1
  local got; got=$(clip); say "clipboard after a plain drag over a line: [$got]"
  check "a plain drag selected the line and copied it on release, with no prompt" test "$got" = "LINE 2988 needle-2988"
  check "and the pane is back on the live screen" live history
  # Shift+drag: the terminal's own selection, copied with Command+C.
  setclip "SENTINEL"
  a=$(textpoint history "LINE 2990 needle-2990" 0 -2); b=$(textpoint history "LINE 2990 needle-2990" 22)
  drag $a $b shift; sleep 0.5; key cmd+c; sleep 0.6
  got=$(clip); say "clipboard after Shift+drag and Command+C: [$got]"
  check "Shift+drag selected the line and Command+C copied exactly it" test "$got" = "LINE 2990 needle-2990"
  shot history-selection
  # A right click offers nothing.
  rclick $p; sleep 0.8
  check "a right click left the pane alone: live, one pane per session, same process" ev "[ \"\$(pfmt history '#{pane_in_mode}')\" = 0 ] && [ \"\$(rq 'priv list-panes -a | wc -l' | tr -d ' ')\" = 4 ] && [ \"\$(rq \"pfmt history '#{pane_pid}'\")\" = $pp ]"
  check "no tmux binding on the private server opens a menu or kills, respawns, or splits a pane" ev "[ \"\$(rq 'priv list-keys -T root' | grep -cE 'display-menu|kill-pane|respawn|split-window|swap-pane')\" = 0 ]"
  shot history-right-click
  check "none of the real input events left a modifier held on the MacBook" hidclean
}

case_link() {
  note "a link printed in a session opens with Command+Shift+click (tmux tracks the mouse there)"
  mark; show history
  check "the link target on the MacBook answers and logs" rq 'curl -s -o /dev/null http://127.0.0.1:$LINKPORT/s15-selfcheck2; sleep 0.5; grep -q "GET /s15-selfcheck2" $R/logs/link.log'
  local n; n=$(rq linkhits)
  local browser; browser=$(rq 'pgrep -x firefox | head -1')
  say "the MacBook's default browser for http is Firefox; running before the click: ${browser:-no}"
  local p; p=$(textpoint history "http://127.0.0.1" 12)
  # An ordinary pointer move first, with nothing held: the state in which
  # modifiers on mouse events alone would not be enough.
  vnc move "$(px 600)" "$(px 300)"; sleep 0.3
  click $p cmd; sleep 2
  check "Command+click alone went to tmux and opened nothing" test "$(rq linkhits)" = "$n"
  mark
  click $p cmd+shift
  check "Command+Shift+click made the app open the link" wait_log "opening a link with scheme http" 8
  # What the MacBook does with the link is its default browser's business:
  # running, it fetches the page; not running, it is started for it and may
  # stop at its own profile chooser, which the driver does not answer.
  check "and the MacBook handed the link to its default browser (fetched from the link target, or the browser was started for it)" waitfor 12 ev "[ \"\$(rq linkhits)\" -gt $n ] || { [ -z '$browser' ] && rq 'pgrep -x firefox' | grep -q .; }"
  facts "link target log, and the browser process" rq 'grep s15-link $R/logs/link.log | tail -2; p=$(pgrep -x firefox | head -1); [ -n "$p" ] && echo "firefox pid $p started $(ps -o lstart= -p $p)"; date'
  # A browser the click started is closed again; one that was already running is left alone.
  if [ -z "$browser" ]; then sleep 2; rq 'pkill -x firefox'; say "the browser the link started was closed again"; fi
  sleep 1; rq 'open "$APP"'; sleep 1.5; click $((PX+400)) $((PY+300)); sleep 0.5
  check "the pane is unchanged" live history
}

case_ime() {
  note "a real input method composes at the cursor and commits to the agent (keys posted as real events on the MacBook)"
  local JP=com.apple.inputmethod.Kotoeri.RomajiTyping.Japanese
  gui "$R/bin/inputsource enabled | sort > $R/ime.before; $R/bin/inputsource current > $R/ime.current" >/dev/null
  facts "input sources before" rq "cat $R/ime.before; echo current: \$(cat $R/ime.current)"
  trap ime_restore EXIT
  for h in claude codex copilot; do
    mark; show $h; clearline $h
    # Enabling the method (its parent, which cannot itself be selected), then selecting its mode.
    gui "$R/bin/inputsource select com.apple.inputmethod.Kotoeri.RomajiTyping" >/dev/null
    gui "$R/bin/inputsource select $JP" >/dev/null; sleep 1.5
    check "$h: the Japanese input method is selected on the MacBook" ev "gui '$R/bin/inputsource current' | grep -q '$JP'"
    hidkeys 40 0; sleep 1
    check "$h: while composing, the agent has received nothing" ev "! shas $h か && ! screen $h | tail -8 | grep -q 'ka'"
    shot ime-$h-composing
    hidkeys 36; sleep 1.2
    check "$h: Return committed the composition and the agent received か" swait $h "か" 8
    check "$h: committing submitted nothing" idle $h
    hidkeys 45 34 4 31 45 45; sleep 0.8
    check "$h: a second composition (nihon) is held back the same way" ev "! shas $h にほん && ! shas $h 日本"
    hidkeys 36; sleep 1.2
    check "$h: and committed: the agent received the converted word after か" waitfor 8 ev "screen $h | grep -qE 'か(日本|にほん)'"
    say "$h: the composer now reads: $(screen $h | grep -oE 'か(日本|にほん)' | head -1)"
    sshot $h "after two compositions"
    shot ime-$h-committed
    gui "$R/bin/inputsource select \$(cat $R/ime.current)" >/dev/null; sleep 1
    hidkeys 6
    check "$h: back on the original layout, a plain key types plainly" waitfor 6 ev "screen $h | grep -qE 'か(日本|にほん)z'"
    check "$h: nothing was submitted" idle $h
    clearline $h
  done
  ime_restore; trap - EXIT
  check "none of the real key events left a modifier held on the MacBook" hidclean
}
ime_restore() {
  gui "[ -f $R/ime.before ] || exit 0; $R/bin/inputsource select \$(cat $R/ime.current) >/dev/null; for id in \$($R/bin/inputsource enabled | sort | comm -13 $R/ime.before -); do $R/bin/inputsource disable \$id; done; grep -q Kotoeri $R/ime.before || { $R/bin/inputsource disable com.apple.inputmethod.Kotoeri.RomajiTyping.Japanese; $R/bin/inputsource disable com.apple.inputmethod.Kotoeri.RomajiTyping; }; $R/bin/inputsource enabled | sort > $R/ime.after" >/dev/null
  rq "cmp -s $R/ime.before $R/ime.after && echo restored || { echo NOT-RESTORED; diff $R/ime.before $R/ime.after; }; rm -f $R/ime.before $R/ime.current" > "$L/ime.restore" 2>&1
  check "the MacBook's input sources are as they were" grep -qx restored "$L/ime.restore"
}

case_priority() {
  note "who gets a key depends on what has the keyboard: the app's shortcuts, the Dashboard, the agent, the session list"
  mark; show claude; clearline claude
  typetext "in-claude"
  check "session terminal focused: plain keys are the agent's" swait claude "in-claude" 6
  key cmd+d; sleep 1.5
  check "Command+D is the app's: the Dashboard took the pane" shown 'workspace: showing view(id: "dashboard")'
  check "and the agent received nothing of it" ev "screen claude | grep -q 'in-claude' && ! screen claude | grep -q 'in-claudd'"
  typetext "?"; sleep 1
  sshot claude "while the Dashboard has the keyboard"
  facts "dashboard after ?" rq 'vscreen dashboard | head -20'
  check "Dashboard focused: a plain key is the Dashboard's (its help opened)" rq 'vscreen dashboard | grep -qi -E "help|keybinding|shortcut"'
  check "and it did not reach the agent" ev "! screen claude | grep -q 'in-claude?'"
  key Escape; sleep 0.5
  shot priority-dashboard
  mark; key cmd+shift+bracketright; sleep 1.5
  check "Command+Shift+] is the app's from the Dashboard too: it selected a session and focused its terminal" shown "workspace: showing terminal"
  local now; now=$(since | sed -n 's/.*showing terminal(runtimeSessionID: "\(rs-[0-9a-f]*\)").*/\1/p' | tail -1)
  local which; which=$(rq "registry | awk '\$1==\"$now\"{print \$4}'")
  say "the session selected by Command+Shift+]: $which"
  clearline "$which"; typetext "kb-after-next"
  check "and the keyboard is in that session's terminal ($which)" swait "$which" "kb-after-next" 6
  clearline "$which"
  # The session list.
  mark; key cmd+shift+l; sleep 0.8
  typetext "zzz"; sleep 0.6
  check "Command+Shift+L gave the keyboard to the session list: typing reaches no agent" ev "for p in claude codex copilot; do screen \$p | grep -q zzz && exit 1; done; ! screen history | tail -3 | grep -q zzz"
  mark; key Down; sleep 1
  local target; target=$(since | sed -n 's/.*showing terminal(runtimeSessionID: "\(rs-[0-9a-f]*\)").*/\1/p' | tail -1)
  target=$(rq "registry | awk '\$1==\"$target\"{print \$4}'")
  check "the Down arrow moved the selection to the next session ($target) and the keyboard stayed in the list" test -n "$target" -a "$target" != "$which"
  typetext "yyy"; sleep 0.5
  check "typing still reaches no agent" ev "! screen $target | grep -q yyy"
  key Return; sleep 1
  clearline "$target"; typetext "kb-after-return"
  check "Return in the session list went into the selected session ($target): typing reaches its agent" swait "$target" "kb-after-return" 6
  clearline "$target"
  # A key the agent binds that resembles an app shortcut: Control+O is Claude's, Command+O is the app's.
  mark; show claude; clearline claude
  key ctrl+o; sleep 1.5
  sshot claude "after Control+O"
  check "Control+O reached Claude (its transcript view opened)" shas claude "transcript"
  check "and the app's Command+O Overview did not open" ev "! since | grep -q 'view(id: \"overview\")'"
  key ctrl+o; sleep 0.8
  # F12 is tmux's detach key: the view lets go, the agent stays.
  local hp; hp=$(harnesspid claude)
  mark; key F12; sleep 2
  check "F12 detached the view (the app shows Detached)" shown "detached(runtimeSessionID"
  check "no terminal is attached to Claude's pane" ev "[ \"\$(rq 'attached claude')\" = 0 ]"
  check "the agent is still running (Studio pid $hp) and never saw F12" alive "$hp"
  shot priority-detached
  # Reattach button: in the middle of the pane, under the message.
  mark; click $((PX+PW/2)) $((PY+PH/2+34)); sleep 2
  check "Reattach attached the terminal again" attached claude
  clearline claude; typetext "kb-after-reattach"
  check "and the keyboard is back with the agent" swait claude "kb-after-reattach" 6
  clearline claude
  # The global next-attention shortcut works from inside a terminal.
  show history; key ctrl+u
  typetext "ask"; key Return
  check "the history session asked for attention (a real PermissionRequest hook)" waitfor 15 ev "rq snapshot | grep history | grep -q 'attention=True'"
  mark; show claude; clearline claude
  typetext "before-hotkey"
  # A global shortcut is a real key event: it is posted on the MacBook itself.
  mark; hidkeys 45:ctrl,opt,cmd; sleep 2
  check "the global next-attention shortcut, pressed while typing to Claude, focused the waiting session" shown "workspace: focus embedded $(rq 'rsof history')"
  check "and Claude received nothing of the chord" ev "screen claude | grep -q 'before-hotkey' && ! screen claude | grep -q 'before-hotkeyn'"
  shot priority-attention
  show claude; clearline claude
}

case_reattach() {
  note "long output and reattachment: the view is replaced, the history and the agents are not"
  local h
  for h in claude codex copilot; do
    eval "hp_$h=$(harnesspid $h); pp_$h=$(panepid $h)"
    mark; show $h
    check "$h: before: the 150-line answer is still there" hasline $h "S15MARK 150"
  done
  local hist; hist=$(pfmt history '#{history_size}')
  note "the window is closed (Command+W): every terminal detaches"
  mark; key cmd+w; sleep 2
  check "the app logged the window closing and detaching its terminals" shown "workspace: closed, detaching"
  check "no client is attached to any pane" ev "[ -z \"\$(rq clients)\" ]"
  for h in claude codex copilot; do
    check "$h: same pane process and same harness with no viewer" ev "[ \"\$(rq \"pfmt $h '#{pane_pid}'\")\" = $(eval echo \$pp_$h) ] && kill -0 $(eval echo \$hp_$h)"
  done
  note "the app is quit and started again: a new viewer process"
  local old; old=$(rq apppid)
  rq "app_quit; app_defaults '$FRAME'; app_start" || say "FAIL: the app did not start again"
  sleep 2
  local new; new=$(rq apppid)
  check "a different app process (was $old, now $new)" test "$old" != "$new"
  for h in claude codex copilot; do
    mark; show $h
    check "$h: the new viewer attached to the same pane" attached $h
    check "$h: the pane process is the same (MacBook pid $(eval echo \$pp_$h)) and so is the harness (Studio pid $(eval echo \$hp_$h))" ev "[ \"\$(rq \"pfmt $h '#{pane_pid}'\")\" = $(eval echo \$pp_$h) ] && kill -0 $(eval echo \$hp_$h)"
    check "$h: the end of the long answer is on screen again" swait $h "S15MARK 150" 10
    vnc key shift+Page_Up shift+Page_Up shift+Page_Up shift+Page_Up shift+Page_Up shift+Page_Up shift+Page_Up shift+Page_Up; sleep 1.2
    check "$h: after reattachment Shift+Page Up still reaches the first lines of the answer" ev "hasline $h 'S15MARK 3' || hasline $h 'S15MARK 2'"
    sshot $h "the top of the transcript after reattachment"
    local i=0; while [ $i -lt 14 ] && ! hasline $h "S15MARK 150"; do vnc key shift+Page_Down; sleep 0.4; i=$((i+1)); done
    check "$h: and Shift+Page Down returns to the end" hasline $h "S15MARK 150"
    clearline $h; typetext "after-reattach"
    check "$h: typing reaches the same agent" swait $h "after-reattach" 6
    clearline $h
  done
  mark; show history
  check "history: tmux still holds all $hist lines after the viewer was replaced" test "$(pfmt history '#{history_size}')" -ge "$hist"
  key cmd+f; sleep 0.8; typetext "needle-0007"; key Return; sleep 0.8
  check "history: Command+F in the new viewer finds line 7: [$(pfmt history '#{copy_cursor_line}')]" test "$(pfmt history '#{copy_cursor_line}')" = "LINE 0007 needle-0007"
  key Escape; sleep 0.5
  shot reattached
}

for c in "$@"; do "case_$c"; done
say ""
say "RESULT: $fail check(s) failed in: $*"
exit $((fail > 0))

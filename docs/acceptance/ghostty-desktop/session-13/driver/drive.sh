#!/bin/bash
# Drives the Agent Mux.app workspace in the isolated world and records what
# was observed. usage: drive.sh OUTDIR CASE...   (cases: fresh env sidebar
# focus attention reconnect retained close; `fresh` restarts the app first)
here=$(cd "$(dirname "$0")" && pwd)
. "$here/env.sh"
OUT=$1; shift
LOG=$OUT/workspace-acceptance.log; SHOTS=$OUT
. "$here/lib.sh"
WORK=$(cat $R/rs-work); PERS=$(cat $R/rs-personal)
PW=$(priv list-panes -a -F '#{pane_id} #{@amx_vars}' >/dev/null 2>&1; python3 - <<PY
import json,os
for s in json.load(open(os.path.expanduser('~/.local/share/agent-mux/runtime-sessions.json')))['sessions']:
    if s['id']=='$WORK': print(s['target']['ref'])
PY
)
PP=$(python3 - <<PY
import json,os
for s in json.load(open(os.path.expanduser('~/.local/share/agent-mux/runtime-sessions.json')))['sessions']:
    if s['id']=='$PERS': print(s['target']['ref'])
PY
)
BETA=$(kwin beta)
APPPID=$(apppid); KPID=$(kittypid)

case_fresh() {
  note "the app under test is started fresh: workspace opened at launch, no terminal attached yet"
  perl -pi -e 's/\*\*Status\*\*: complete/**Status**: pending/' $R/plans/demo/session-1.md
  rm -f $R/logs/worker.log $R/logs/ask.json $R/logs/ask.err $R/logs/aslrun.log
  app_quit; app_defaults; app_start
  APPPID=$(apppid)
  # Kept to the left of the screen, clear of anything the desktop shows in
  # its middle, so the screenshots hold the app's windows only.
  ax "set position of $WS to {10, 40}" >/dev/null; ax "set size of $WS to {800, 600}" >/dev/null
  check "the app subscribed to the world's daemon" logged "feed: live"
  check "the workspace opened" logged "workspace: opened"
  check "nothing is attached to the private tmux server" test -z "$(clients)"
  facts "app log since launch" applog
}

case_env() {
  note "environment"
  facts "host" sw_vers
  facts "amx under test (this checkout, built into the world)" amx --version
  facts "app under test" sh -c "/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' \"$APP/Contents/Info.plist\"; echo pid $APPPID; ps -p $APPPID -o command="
  facts "the app's preferences (its own domain)" defaults read $BUNDLE
  facts "kitty (separate instance, pid $KPID, socket $(ksock))" /Applications/kitty.app/Contents/MacOS/kitty --version
  facts "tmux" tmux -V
  facts "asl (the installed one, linked into the world)" sh -c 'asl doctor --help >/dev/null 2>&1 && echo "asl answers on the world PATH"'
  facts "world daemon" sh -c 'ps -axo pid,command | grep "[a]mx daemon start" | grep -v /Users/'
  facts "runtime-session registry" registry
  facts "private tmux server panes" priv list-panes -a -F '#{session_name} #{pane_id} pid=#{pane_pid} dead=#{pane_dead}'
  facts "kitty tree" tree
  say "sessions: alpha/work = $WORK on tmux $PW; alpha/personal = $PERS on tmux $PP; beta/work = Kitty window $BETA"
}

settle() { # every agent back to working, nothing waiting
  tell_tmux $PW work; tell_tmux $PP work; tell_kitty $BETA work
  waitfor 10 sh -c ". $here/env.sh; . $here/lib.sh; ! snapshot | grep -q 'attention=True'"
}

case_sidebar() {
  note "S1 project-profile-sidebar: the sidebar groups the daemon's sessions by project and labels each with its profile"
  front $APPPID; settle
  facts "the daemon's v2 snapshot" snapshot
  facts "sidebar as accessibility reads it (' > ' marks the selected row)" sidebar
  check "row 1 is the project line alpha" row_is 1 "alpha"
  check "row 2 is alpha's session on profile personal" row_is 2 "alpha, personal, working"
  check "row 3 is alpha's session on profile work" row_is 3 "alpha, work, working"
  check "row 4 is the project line beta" row_is 4 "beta"
  check "row 5 is beta's session on profile work, marked as a Kitty window" sh -c ". $here/env.sh; . $here/lib.sh; sidebar | sed -n 5p | grep -q 'beta, work,.*Kitty'"
  check "there are exactly five rows" test "$(sidebar | grep -c .)" = 5

  note "S2 attention: a tmux session and the Kitty session each raise a permission prompt"
  tell_tmux $PP ask
  check "alpha/personal reads as needing attention" waitfor 10 row_is 2 "alpha, personal, needs attention"
  tell_kitty $BETA ask
  check "beta/work reads as needing attention" waitfor 10 row_is 5 "beta, work, needs attention"
  facts "sidebar" sidebar
  facts "alpha project line: name and waiting count" cell_texts 1
  facts "alpha/personal row: glyph, profile, detail" cell_texts 2
  facts "alpha/work row: glyph, profile, detail" cell_texts 3
  facts "beta project line: name and waiting count" cell_texts 4
  facts "beta/work row: glyph, profile, detail" cell_texts 5
  check "alpha's line counts one waiting session" sh -c ". $here/env.sh; . $here/lib.sh; cell_texts 1 | grep -q 'alpha, 1'"
  check "alpha/personal shows the key glyph" sh -c ". $here/env.sh; . $here/lib.sh; cell_texts 2 | grep -q '🔑, personal'"
  check "alpha/work still shows the working glyph" sh -c ". $here/env.sh; . $here/lib.sh; cell_texts 3 | grep -q '⚡, work'"
  check "beta/work shows the key glyph and its Kitty mark" sh -c ". $here/env.sh; . $here/lib.sh; cell_texts 5 | grep -q '🔑, work, .*Kitty'"
  facts "status item title (attention counts in the menu bar)" ax 'get title of menu bar item 1 of menu bar 2'
  shot sidebar-attention
  settle
  check "after both agents resume, no row needs attention" sh -c ". $here/env.sh; . $here/lib.sh; ! sidebar | grep -q 'needs attention'"
}

case_focus() {
  note "F1 mixed-backend-focus: a click on a tmux session embeds its terminal and gives it the keyboard"
  front $APPPID; settle
  K focus-tab --match id:1 >/dev/null
  facts "tmux clients before (expect none or earlier ones)" clients
  mark; clickrow 3
  check "the app attached a terminal to alpha/work" waitfor 10 since_has "attached a terminal to $WORK"
  check "the private tmux server has a Ghostty client on alpha/work's pane" waitfor 10 sh -c ". $here/env.sh; . $here/lib.sh; clients | grep -q 'pane=$PW term=xterm-ghostty'"
  check "the registry records the attachment" sh -c ". $here/env.sh; . $here/lib.sh; registry | grep -q '$WORK live alpha tmux $PW attached'"
  check "the sidebar selection is alpha/work" sh -c ". $here/env.sh; . $here/lib.sh; selected_row | grep -q 'alpha, work'"
  check "the window is titled for the session" test "$(wtitle)" = "alpha — work"
  typekeys "typed-into-work"
  check "keys typed with no further click reach alpha/work's agent" waitfor 5 pane_has $PW "]: typed-into-work"
  facts "alpha/work pane" sh -c ". $here/env.sh; . $here/lib.sh; priv capture-pane -p -t $PW | grep -v '^\$' | tail -3"
  facts "tmux clients" clients
  facts "app log" since

  note "F2 a second tmux session gets its own terminal; the first stays attached, hidden"
  mark; clickrow 2
  check "the app attached a terminal to alpha/personal" waitfor 10 since_has "attached a terminal to $PERS"
  check "both panes have a Ghostty client" waitfor 10 sh -c ". $here/env.sh; . $here/lib.sh; test \$(clients | grep -c 'term=xterm-ghostty') = 2"
  typekeys "typed-into-personal"
  check "keys now reach alpha/personal's agent" waitfor 5 pane_has $PP "]: typed-into-personal"
  check "and not alpha/work's" sh -c ". $here/env.sh; . $here/lib.sh; ! pane_has $PW typed-into-personal"
  before=$(clients)
  mark; clickrow 3
  check "coming back to alpha/work focuses it" waitfor 10 since_has "focus embedded $WORK"
  check "without attaching again (same clients)" test "$(clients)" = "$before"
  check "no second terminal was made for it" sh -c ". $here/env.sh; . $here/lib.sh; ! since | grep -q 'attached a terminal'"
  typekeys "back-in-work"
  check "keys reach alpha/work again" waitfor 5 pane_has $PW "]: back-in-work"
  facts "tmux clients" clients
  shot focus-embedded

  note "F3 a click on the Kitty session raises Kitty on its window and embeds nothing"
  facts "kitty tree before (tab 1 active, beta's tab not)" tree
  check "before the click the app has the keyboard" test "$(frontpid)" = "$APPPID"
  mark; clickrow 5
  check "the app focused the Kitty window externally" waitfor 10 since_has "focus Kitty window $BETA externally"
  check "Kitty focused beta's window" waitfor 10 sh -c ". $here/env.sh; . $here/lib.sh; test \"\$(kfocused)\" = $BETA"
  check "the separate Kitty instance (pid $KPID) came to the front" waitfor 10 sh -c ". $here/env.sh; . $here/lib.sh; test \"\$(frontpid)\" = $KPID"
  check "no terminal was attached for it (same tmux clients)" test "$(clients)" = "$before"
  facts "kitty tree after" tree
  facts "app log" since
  front $APPPID
  facts "the pane beside the sidebar" texts
  check "the pane says the session runs in Kitty" sh -c ". $here/env.sh; . $here/lib.sh; texts | grep -q 'beta runs in Kitty'"
  check "the sidebar selection is beta/work" sh -c ". $here/env.sh; . $here/lib.sh; selected_row | grep -q 'beta, work'"
  shot focus-kitty
}

case_attention() {
  note "A1 next-attention: three sessions wait, oldest first alpha/personal (tmux), beta/work (Kitty), alpha/work (tmux)"
  front $APPPID; settle
  tell_tmux $PP ask; sleep 1.5; tell_kitty $BETA ask; sleep 1.5; tell_tmux $PW ask
  check "all three need attention" waitfor 10 sh -c ". $here/env.sh; . $here/lib.sh; test \$(snapshot | grep -c 'attention=True') = 3"
  facts "snapshot" snapshot
  K focus-tab --match id:1 >/dev/null; front $KPID
  check "Kitty has the keyboard, on tab 1, before the first press" sh -c ". $here/env.sh; . $here/lib.sh; test \"\$(frontpid)\" = $KPID && test \"\$(kfocused)\" = 1"

  note "A2 global shortcut ⌃⌥⇧⌘N pressed over Kitty: the tmux session that waited longest opens in the workspace"
  mark; hotkey 45
  check "the app picked alpha/personal's pane" waitfor 10 since_has "next attention: tmux $PP (key)"
  check "the workspace focused its embedded terminal" waitfor 10 since_has "focus embedded $PERS"
  check "the app came to the front" waitfor 10 sh -c ". $here/env.sh; . $here/lib.sh; test \"\$(frontpid)\" = $APPPID"
  check "the sidebar selection is alpha/personal" sh -c ". $here/env.sh; . $here/lib.sh; selected_row | grep -q 'alpha, personal'"
  check "alpha/personal's pane has a Ghostty client" waitfor 10 sh -c ". $here/env.sh; . $here/lib.sh; clients | grep -q 'pane=$PP term=xterm-ghostty'"
  facts "app log" since

  note "A3 second press: the Kitty session is raised in Kitty"
  mark; hotkey 45
  check "the app picked beta's Kitty window" waitfor 10 since_has "next attention: window $BETA (key)"
  check "Kitty focused beta's window" waitfor 10 sh -c ". $here/env.sh; . $here/lib.sh; test \"\$(kfocused)\" = $BETA"
  check "Kitty came to the front" waitfor 10 sh -c ". $here/env.sh; . $here/lib.sh; test \"\$(frontpid)\" = $KPID"
  facts "app log" since

  note "A4 third press, over Kitty: the other tmux session opens in the workspace"
  mark; hotkey 45
  check "the app picked alpha/work's pane" waitfor 10 since_has "next attention: tmux $PW (key)"
  check "the workspace focused its embedded terminal" waitfor 10 since_has "focus embedded $WORK"
  check "the app came to the front" waitfor 10 sh -c ". $here/env.sh; . $here/lib.sh; test \"\$(frontpid)\" = $APPPID"
  check "the sidebar selection is alpha/work" sh -c ". $here/env.sh; . $here/lib.sh; selected_row | grep -q 'alpha, work'"
  typekeys "typed-after-next-attention"
  check "the keyboard is in alpha/work's terminal" waitfor 5 pane_has $PW "]: typed-after-next-attention"
  facts "app log" since
  shot next-attention

  note "A5 fourth press wraps to the oldest; then the Session menu's Next Needing Attention continues the same cycle"
  mark; hotkey 45
  check "the cycle wrapped to alpha/personal" waitfor 10 since_has "next attention: tmux $PP (key)"
  check "the sidebar selection is alpha/personal" waitfor 5 sh -c ". $here/env.sh; . $here/lib.sh; selected_row | grep -q 'alpha, personal'"
  mark; ax 'click menu item "Next Needing Attention" of menu 1 of menu bar item "Session" of menu bar 1' >/dev/null
  check "the menu item went on to beta's Kitty window" waitfor 10 since_has "next attention: window $BETA (key)"
  check "Kitty came to the front" waitfor 10 sh -c ". $here/env.sh; . $here/lib.sh; test \"\$(frontpid)\" = $KPID"
  facts "app log" since

  note "A6 a session that stops waiting leaves the cycle"
  tell_tmux $PW work
  check "alpha/work no longer needs attention" waitfor 10 sh -c ". $here/env.sh; . $here/lib.sh; test \$(snapshot | grep -c 'attention=True') = 2"
  mark; hotkey 45
  check "the next press skips it and wraps to alpha/personal" waitfor 10 since_has "next attention: tmux $PP (key)"
  mark; hotkey 45
  check "then beta again, never alpha/work" waitfor 10 since_has "next attention: window $BETA (key)"
  settle
  mark; front $APPPID; hotkey 45; sleep 1.5
  check "with nothing waiting the shortcut focuses nothing" since_has "next attention: nothing is waiting"
  facts "app log" since
}

case_reconnect() {
  note "R1 daemon-reconnect: alpha/work is selected with the keyboard in its terminal, alpha/personal attached behind it"
  front $APPPID; settle
  clickrow 2; sleep 1; clickrow 3
  check "alpha/work is selected" waitfor 10 sh -c ". $here/env.sh; . $here/lib.sh; selected_row | grep -q 'alpha, work'"
  check "both tmux sessions have a Ghostty client" waitfor 10 sh -c ". $here/env.sh; . $here/lib.sh; test \$(clients | grep -c 'term=xterm-ghostty') = 2"
  before_clients=$(clients); before_sidebar=$(sidebar | sed 's/ · [0-9<]*[mhd]//')
  facts "tmux clients before" clients
  facts "sidebar before" sidebar
  facts "agent processes before" priv list-panes -a -F '#{pane_id} pid=#{pane_pid} dead=#{pane_dead}'
  agents=$(priv list-panes -a -F '#{pane_id} pid=#{pane_pid} dead=#{pane_dead}')

  note "R2 the daemon stops"
  mark; daemon_stop
  check "the feed reports the daemon gone" waitfor 15 since_has "feed: notRunning"
  check "the workspace holds its rows and its selection" waitfor 5 since_has "rows held from the last snapshot (3 session(s), selection $WORK)"
  check "the sidebar lists the same sessions with the same selection" test "$(sidebar | sed 's/ · [0-9<]*[mhd]//')" = "$before_sidebar"
  check "the sidebar says why it is not current" sh -c ". $here/env.sh; . $here/lib.sh; texts | grep -q 'Daemon: not running'"
  check "both terminals are still attached (same tmux clients)" test "$(clients)" = "$before_clients"
  check "no terminal was detached" sh -c ". $here/env.sh; . $here/lib.sh; ! since | grep -q 'detach'"
  typekeys "typed-while-daemon-down"
  check "the selected terminal still takes keys with no daemon" waitfor 5 pane_has $PW "]: typed-while-daemon-down"
  facts "sidebar while the daemon is down" sidebar
  facts "pane and footer texts" texts
  shot daemon-down
  clickrow 2
  check "another held row can still be selected and typed into" waitfor 5 sh -c ". $here/env.sh; . $here/lib.sh; selected_row | grep -q 'alpha, personal'"
  typekeys "personal-while-daemon-down"
  check "its keys reach alpha/personal" waitfor 5 pane_has $PP "]: personal-while-daemon-down"
  clickrow 3
  check "back on alpha/work" waitfor 5 sh -c ". $here/env.sh; . $here/lib.sh; selected_row | grep -q 'alpha, work'"
  facts "app log while down" since

  note "R3 the daemon starts again"
  mark; daemon_start
  check "the feed is live again" waitfor 20 since_has "feed: live"
  check "the rows are current again with the selection kept" waitfor 10 since_has "rows current (3 session(s), selection $WORK)"
  sleep 2
  check "the sidebar lists the same sessions, grouped and labeled as before, with the same selection" test "$(sidebar | sed 's/ · [0-9<]*[mhd]//')" = "$before_sidebar"
  check "the not-current notice is gone" sh -c ". $here/env.sh; . $here/lib.sh; ! texts | grep -q 'Daemon:'"
  check "both terminals are the same attachments as before the restart" test "$(clients)" = "$before_clients"
  check "the agents are the same processes" test "$(priv list-panes -a -F '#{pane_id} pid=#{pane_pid} dead=#{pane_dead}')" = "$agents"
  check "nothing was attached again or detached" sh -c ". $here/env.sh; . $here/lib.sh; ! since | grep -Eq 'attached a terminal|detach|no longer listed'"
  typekeys "typed-after-reconnect"
  check "the keyboard is still in alpha/work's terminal" waitfor 5 pane_has $PW "]: typed-after-reconnect"
  facts "snapshot from the new daemon" snapshot
  facts "sidebar after" sidebar
  facts "tmux clients after" clients
  facts "app log across the restart" since
  shot daemon-back

  note "R4 attention raised after the reconnect still reaches the same rows"
  tell_tmux $PP ask
  check "alpha/personal reads as needing attention" waitfor 10 row_is 2 "alpha, personal, needs attention"
  settle
}

case_retained() {
  note "M1 retained menu bar companion: with the workspace open, the status item still lists the sessions and its actions"
  front $APPPID; settle
  tell_tmux $PP ask
  waitfor 10 row_is 2 "alpha, personal, needs attention"
  facts "status item title" ax 'get title of menu bar item 1 of menu bar 2'
  statusmenu_open
  statusmenu_items > $R/menu.txt
  facts "status menu items ('----' is a separator; each session row is followed by its ⌥ Mute alternate)" cat $R/menu.txt
  for want in "Needs attention" "Working" "Open Workspace" "Open Dashboard" "Launch Project" "Recent" "Notifications" "Preferences…" "Quit Agent Mux"; do
    check "the menu has: $want" grep -qxF "$want" $R/menu.txt
  done
  check "the waiting tmux session is listed with its glyph" grep -q '^🔑 alpha · claude' $R/menu.txt
  check "the Kitty session is listed" grep -q '^⚡ beta · claude' $R/menu.txt
  row=$(grep -n '^🔑 alpha' $R/menu.txt | head -1 | cut -d: -f1)
  [ -n "$row" ] || { menu_close; say "ABORT M1: the session row is not in the menu"; return; }
  mark; statusmenu_click $row; sleep 1
  check "clicking the tmux session's menu row focuses it in the workspace" waitfor 10 since_has "focus embedded $PERS"
  check "the sidebar selection follows" waitfor 5 sh -c ". $here/env.sh; . $here/lib.sh; selected_row | grep -q 'alpha, personal'"
  facts "app log" since
  settle

  note "M2 retained shortcut: the dashboard shortcut ⌃⌥⇧⌘D still opens the dashboard in Kitty"
  K focus-tab --match id:1 >/dev/null; front $APPPID
  mark; hotkey 2
  check "the app reports the dashboard opened or focused" waitfor 20 sh -c ". $here/env.sh; . $here/lib.sh; since | grep -Eq 'dashboard (opened|focused)'"
  check "Kitty came to the front" waitfor 10 sh -c ". $here/env.sh; . $here/lib.sh; test \"\$(frontpid)\" = $KPID"
  facts "kitty tree (the dashboard tab)" tree
  facts "app log" since
  dash=$(kfocused); K send-text --match "id:$dash" q; sleep 1

  note "M3 retained ASL answer panel: a real asl run inside the tmux session alpha/work asks its supervisor"
  front $APPPID
  tell_tmux $PW asl
  check "the worker turn started" waitfor 60 test -s $R/logs/worker.log
  qid=""
  for i in $(seq 1 12); do
    out=$(AMX_RUNTIME_SESSION_ID=$WORK asl outbox wait $R/plans/demo --timeout 10 --json 2>/dev/null)
    qid=$(printf '%s' "$out" | python3 -c "import json,sys; q=json.load(sys.stdin).get('questions') or []; print(q[0]['id'] if q else '')" 2>/dev/null)
    [ -n "$qid" ] && break
  done
  say "the supervisor surfaced question: ${qid:-none}"
  check "a question was surfaced by asl outbox wait run as the session" test -n "$qid"
  [ -n "$qid" ] || { say "ABORT M3: no question to answer"; return; }
  check "the daemon lists the run as waiting on the tmux session" waitfor 20 sh -c ". $here/env.sh; . $here/lib.sh; snapshot | grep -q 'asl waiting run=.* unanswered=1 runtime=$WORK'"
  facts "snapshot" snapshot
  check "the sidebar row shows the question" waitfor 10 row_is 3 "alpha, work, needs attention"
  facts "alpha/work row" cell_texts 3
  statusmenu_open
  statusmenu_items > $R/menu.txt
  facts "status menu items" cat $R/menu.txt
  check "the menu offers Answer… under the tmux session" grep -qx 'Answer…' $R/menu.txt
  ans=$(grep -nx 'Answer…' $R/menu.txt | head -1 | cut -d: -f1)
  [ -n "$ans" ] || { menu_close; say "ABORT M3: no Answer… item"; return; }
  mark; statusmenu_click $ans; sleep 2
  check "the app opened the answer panel on the run" waitfor 10 since_has "answer panel: run "
  facts "the app's windows" ax 'get name of every window'
  ax "get name of $PANEL" >/dev/null 2>&1 || { say "ABORT M3: the panel is not open"; return; }
  check "the panel shows the plan and the worker's question" waitfor 15 sh -c ". $here/env.sh; . $here/lib.sh; panel_texts | grep -q 'Proceed with the fixture change'"
  facts "answer panel texts" panel_texts
  facts "answer panel buttons enabled (Close, Reply, Continue, Pause): Reply is off until there is text" ax "get enabled of every button of group 1 of $PANEL"
  check "the workspace is still open behind the panel, on the same session" sh -c ". $here/env.sh; . $here/lib.sh; selected_row | grep -q 'alpha, personal'"
  ax "set position of $PANEL to {150, 90}" >/dev/null; sleep 0.5
  shot asl-answer-panel "$PANEL"
  TA="text area 1 of scroll area 2 of group 1 of $PANEL"
  ax "set focused of $TA to true" >/dev/null; sleep 0.5
  typetext "Go ahead, from the answer panel."
  sleep 0.5
  check "the reply editor holds the typed text" test "$(ax "get value of $TA")" = "Go ahead, from the answer panel."
  check "Reply is enabled now" sh -c ". $here/env.sh; . $here/lib.sh; ax 'get enabled of button 2 of group 1 of $PANEL' | grep -q true"
  mark; ax "click button 2 of group 1 of $PANEL" >/dev/null
  check "the app sent the reply to the daemon and it was accepted" waitfor 30 sh -c ". $here/env.sh; . $here/lib.sh; since | grep -q 'asl_action .*: accepted'"
  check "the worker's ask returned with the reply typed in the panel" waitfor 60 grep -q "Go ahead, from the answer panel." $R/logs/ask.json
  check "the worker went on (its ask exited 0)" waitfor 30 grep -q "ask returned 0" $R/logs/worker.log
  check "the daemon no longer lists the run as waiting" waitfor 30 sh -c ". $here/env.sh; . $here/lib.sh; ! snapshot | grep -q 'asl waiting'"
  check "the panel closed itself" waitfor 15 sh -c ". $here/env.sh; . $here/lib.sh; ! ax 'get name of every window' | grep -q 'Supervisor question'"
  facts "the worker's ask result" cat $R/logs/ask.json
  facts "worker log" cat $R/logs/worker.log
  facts "app log" since
  shot workspace-after-answer
}

case_close() {
  note "C1 closing the workspace detaches every view and ends no agent; the menu bar companion remains"
  front $APPPID
  agents=$(priv list-panes -a -F '#{pane_id} pid=#{pane_pid} dead=#{pane_dead}')
  facts "tmux clients before" clients
  mark; ax 'click button 1 of (first window whose name contains "alpha" or name is "Agent Mux")' >/dev/null
  check "the workspace closed and let go of its terminals" waitfor 10 since_has "workspace: closed, detaching"
  check "no client is attached to the private tmux server" waitfor 10 sh -c ". $here/env.sh; . $here/lib.sh; test -z \"\$(clients)\""
  check "the agents are the same live processes" test "$(priv list-panes -a -F '#{pane_id} pid=#{pane_pid} dead=#{pane_dead}')" = "$agents"
  check "the registry still records both runtime sessions live" sh -c ". $here/env.sh; . $here/lib.sh; test \$(registry | grep -c ' live alpha tmux ') = 2"
  facts "registry" registry
  facts "app log" since
  facts "the app's menu bars after closing (the status item's)" ax 'get title of menu bar item 1 of menu bar (count of menu bars)'
  note "C2 Open Workspace in the status menu brings it back on the remembered session"
  statusmenu_open; statusmenu_items > $R/menu.txt
  n=$(grep -nx 'Open Workspace' $R/menu.txt | cut -d: -f1)
  [ -n "$n" ] || { menu_close; say "ABORT C2: no Open Workspace item"; return; }
  mark; statusmenu_click $n
  check "the workspace opened again" waitfor 10 since_has "workspace: opened"
  check "it attached the remembered session's terminal again" waitfor 10 since_has "attached a terminal to"
  facts "sidebar" sidebar
  facts "tmux clients" clients
  facts "app log" since
}

for c in "$@"; do case_$c; done
printf '\n##### %s: %d failed check(s)\n' "$*" "$fail" >> "$LOG"
echo "failed checks: $fail"

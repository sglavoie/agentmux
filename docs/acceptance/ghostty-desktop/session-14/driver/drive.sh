#!/bin/bash
# Drives the Agent Mux.app workspace's Dashboard and utility views in the
# isolated, Kitty-free world and records what was observed.
# usage: drive.sh OUTDIR CASE...
#   cases, in the order a full run takes them:
#   fresh env launch focus overview pager shell editor isolation workflows nokitty
here=$(cd "$(dirname "$0")" && pwd)
. "$here/env.sh"
OUT=$1; shift
LOG=$OUT/desktop-acceptance.log; SHOTS=$OUT
. "$here/lib.sh"
BETA=$(cat $R/rs-beta)
APPPID=$(apppid)

# dash: shows the workspace's Dashboard view and waits for it to have the pane.
dash() { menu View Dashboard; waitfor 5 sh -c ". $here/env.sh; . $here/lib.sh; selected_row | grep -q 'Dashboard, dashboard view'"; }
# The session rows the dashboard lists, top to bottom, by project.
drows() { vscreen dashboard | awk -F'│' '{print $1}' | awk '$1 ~ /^(alpha|beta)$/ {print $1} $1=="▌" && $2 ~ /^(alpha|beta)$/ {print $2}'; }
# The project of the row the dashboard has selected, as its inspector names it.
dselected() { vscreen dashboard | sed -n '3p' | awk -F'│' '{print $2}' | awk '{print $1}'; }
# dsel PROJECT: steps the dashboard's selection, down and then up (the list
# does not wrap), until it is on a row of PROJECT.
dsel() {
  local i=0 key=j
  while [ $i -lt 24 ]; do
    [ "$(dselected)" = "$1" ] && return 0
    [ $i -ge 8 ] && key=k
    vkey dashboard $key; sleep 0.5; i=$((i+1))
  done
  return 1
}
# dact PROJECT: opens the action menu on a dashboard row of PROJECT.
dact() { dash && dsel "$1" && vkey dashboard a && vwait dashboard "Actions for $1" 8; }
# palette TEXT: runs a command-palette entry of the dashboard.
palette() { vkey dashboard :; sleep 0.5; vtype dashboard "$1"; sleep 0.6; vkey dashboard Enter; }
count_live() { live | wc -l | tr -d ' '; }
newest() { live | tail -1; }
snap_has() { snapshot | grep -qE -- "$1"; }
wait_snap() { waitfor "${2:-10}" sh -c ". $here/env.sh; . $here/lib.sh; snapshot | grep -qE -- \"$1\""; }
# wait_log TEXT [SECONDS]: waits for an app log line after the last mark. When
# it never comes, the dashboard's screen goes into the log to say why.
wait_log() {
  waitfor "${2:-10}" sh -c ". $here/env.sh; . $here/lib.sh; since_has \"$1\"" && return 0
  vshot dashboard "the dashboard when '$1' was not logged"
  return 1
}
wait_title() { waitfor "${2:-10}" sh -c ". $here/env.sh; . $here/lib.sh; wtitle | grep -qF -- \"$1\""; }
attached_to() { clients | grep -qF "pane=$1 "; }
# released ID: the workspace let go of a session's terminal after the last
# mark, either because its attach ended with the agent or because a snapshot
# stopped listing the session, whichever the app saw first.
released() { since | grep -qE "(the terminal for $1 ended; detached|$1 is no longer listed; detaching its terminal)"; }
# showrow TEXT: a real click on the sidebar row whose description holds TEXT.
showrow() { front $APPPID; clickrow "$(rowindex "$1")"; waitfor 5 sh -c ". $here/env.sh; . $here/lib.sh; selected_row | grep -qF -- \"$1\""; }

case_fresh() {
  note "the app under test starts with the workspace open; its environment carries the identity of the live session beta"
  app_quit; obs kill-server 2>/dev/null; rm -f $R/logs/views.log; app_defaults
  app_start "$BETA"; APPPID=$(apppid)
  ax "set position of $WS to {10, 40}" >/dev/null; ax "set size of $WS to {1060, 660}" >/dev/null
  check "the app subscribed to the world's daemon" logged "feed: live"
  check "the workspace opened" logged "workspace: opened"
  check "it landed on the Dashboard view and started its terminal" waitfor 15 logged "workspace: started view dashboard"
  check "the Dashboard is the existing TUI, drawn in that terminal" vwait dashboard "Sessions" 20
  check "the TUI lists the tmux session beta" vwait dashboard "beta" 10
  check "no terminal is attached to any agent yet" test -z "$(clients)"
  facts "app log since launch" applog
  facts "sidebar (accessibility)" sidebar
  vshot dashboard "the embedded Dashboard after launch"
  shot dashboard-embedded
}

case_env() {
  note "environment: no Kitty in this world"
  facts "host" sw_vers
  facts "amx under test (this checkout, built into the world)" amx --version
  facts "app under test" sh -c "/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' \"$APP/Contents/Info.plist\"; echo pid $APPPID; ps -p $APPPID -o command="
  facts "the app's preferences (its own domain)" defaults read $BUNDLE
  facts "tmux" tmux -V
  facts "world daemon" sh -c 'ps -axo pid,command | grep "[a]mx daemon start" | grep -v /Users/'
  facts "daemon health: subscribers and Kitty" health
  facts "runtime-session registry" registry
  facts "private tmux server panes (agents only)" priv list-panes -a -F '#{session_name} #{pane_id} pid=#{pane_pid} dead=#{pane_dead}'
  facts "observer tmux sessions (utility views only)" obs list-sessions -F '#{session_name} attached=#{session_attached}'
  facts "process tree under the app" apptree
  facts "what the app's terminals inherited, as logged by the shim before amx ran" cat $R/logs/views.log
  check "the daemon has no Kitty (kitty_snapshot.ready=False)" sh -c ". $here/env.sh; . $here/lib.sh; health | grep -q 'kitty_snapshot.ready=False'"
  check "the app is the one subscriber hosting workspace views" sh -c ". $here/env.sh; . $here/lib.sh; health | grep -c 'workspace_views=True' | grep -qx 1"
  check "the app's terminal runs the shim's 'workspace-view dashboard'" sh -c ". $here/env.sh; . $here/lib.sh; apptree | grep -q \"'workspace-view' 'dashboard'\""
  check "which is 'amx workspace-view dashboard' in the observer" sh -c ". $here/env.sh; . $here/lib.sh; viewcmd dashboard | grep -q 'bin/amx workspace-view dashboard'"
  check "the Dashboard's terminal inherited the borrowed session identity" grep -q "dashboard view: .*AMX_RUNTIME_SESSION_ID=$BETA KITTY_WINDOW_ID=999" $R/logs/views.log
  check "yet the dashboard launched nothing through Kitty and drew its sessions from tmux" vhas dashboard "beta"
}

case_launch() {
  note "launch from the embedded Dashboard: the daemon routes the new agent to its native view (W01, W18)"
  local before=$(count_live)
  dash; mark
  vkey dashboard l; check "the launcher opened in the Dashboard" vwait dashboard "Launch Sessions" 8
  vshot dashboard "launcher"
  vkey dashboard Space; sleep 0.3; vkey dashboard Enter
  check "the dashboard reports the launch and stays up" vwait dashboard "alpha" 20
  ALPHA=$(newest); PA=$(paneof $ALPHA); echo "$ALPHA" > $R/rs-alpha
  say "launched: alpha/work = $ALPHA on tmux $PA"
  check "one more live runtime session is registered, on tmux" test "$(count_live)" = $((before+1))
  check "the workspace was asked to show it once listed (await)" wait_log "awaiting just-launched session $ALPHA" 10
  check "the workspace showed it when the daemon listed it" wait_log "awaited session $ALPHA is listed; showing it" 15
  check "by attaching a terminal of its own to it" wait_log "workspace: attached a terminal to $ALPHA" 10
  check "the window is on the session's native view" wait_title "alpha — work" 10
  check "the sidebar selection is the alpha session row" sh -c ". $here/env.sh; . $here/lib.sh; selected_row | grep -q '^alpha, work'"
  check "exactly one client is attached to alpha's pane" test "$(clients | grep -c "pane=$PA ")" = 1
  check "the Dashboard view kept running behind it" vlive dashboard
  check "and lists both sessions" test "$(drows | sort | tr '\n' ' ')" = 'alpha beta ' 
  facts "app log for the launch" since
  facts "registry" registry
  facts "private tmux clients" clients
  facts "sidebar" sidebar
  shot launch-native-view

  note "the native view has the keyboard: a line typed into the app reaches the agent, and its hook shows in the sidebar"
  front $APPPID; typekeys "ask"
  check "the typed line reached alpha's agent" waitfor 10 pane_has $PA "asked (1)"
  check "the daemon lists alpha as needing permission" wait_snap "alpha .*icon=key .*attention=True" 10
  check "the sidebar says so on the session row" waitfor 10 sh -c ". $here/env.sh; . $here/lib.sh; sidebar | grep -q 'alpha, work, needs attention'"
  check "the project heading counts one waiting" waitfor 5 sh -c ". $here/env.sh; . $here/lib.sh; sidebar | grep -q 'alpha, 1 waiting'"
  facts "alpha's pane" priv capture-pane -p -t $PA
  facts "snapshot" snapshot
  facts "sidebar" sidebar
  shot launch-attention
  tell_tmux $PA work; wait_snap "alpha .*attention=False" 10
}

case_focus() {
  note "focus from the embedded Dashboard: Enter on a row goes through the daemon to that session's native view (W03, W18)"
  ALPHA=$(cat $R/rs-alpha); PA=$(paneof $ALPHA); local PB=$(paneof $BETA)
  dash; dsel beta; mark
  vkey dashboard Enter
  check "the workspace received a view request for beta" wait_log "workspace: view request for session $BETA" 10
  check "and attached its own terminal to beta" wait_log "workspace: attached a terminal to $BETA" 10
  check "the window is on beta's native view" wait_title "beta — work" 10
  check "beta's pane has exactly one client" test "$(clients | grep -c "pane=$PB ")" = 1
  check "alpha's view stayed attached behind it" attached_to $PA
  check "the Dashboard did not take over its own terminal: it still shows the list" vhas dashboard "> Sessions"
  check "the dashboard process has no child: it attached nothing itself" test -z "$(pgrep -P "$(viewpid dashboard)")"
  facts "app log for the focus" since
  facts "private tmux clients" clients
  facts "process tree under the app" apptree
  shot focus-native-view

  note "focusing a session that already has a view makes no second attachment"
  dash; dsel alpha; mark; vkey dashboard Enter
  check "the workspace received a view request for alpha" wait_log "workspace: view request for session $ALPHA" 10
  check "the window is back on alpha's view" wait_title "alpha — work" 10
  check "no new terminal was attached" sh -c ". $here/env.sh; . $here/lib.sh; ! since_has 'attached a terminal'"
  check "alpha's pane still has exactly one client" test "$(clients | grep -c "pane=$PA ")" = 1
}

case_overview() {
  note "the Overview is a utility view of its own, and its focus goes to the native session view (W20)"
  ALPHA=$(cat $R/rs-alpha); mark
  menu View Overview
  check "the workspace started the Overview view" wait_log "workspace: started view overview" 10
  check "it is the live session overview, with a tile per tmux session" vwait overview "alpha" 20
  check "including beta" vwait overview "beta" 10
  check "the sidebar selection is the Overview" sh -c ". $here/env.sh; . $here/lib.sh; selected_row | grep -q 'Overview, overview view'"
  check "it runs as 'amx workspace-view overview'" sh -c ". $here/env.sh; . $here/lib.sh; viewcmd overview | grep -q 'bin/amx workspace-view overview'"
  sleep 3; vshot overview "tiles with previews captured from the tmux panes"
  shot overview-view
  local before=$(clients | wc -l | tr -d ' ')
  check "previewing attached nothing: the client list is unchanged" test "$(clients | wc -l | tr -d ' ')" = "$before"
  mark; vkey overview Enter
  check "Enter on a tile sent a view request to the workspace" wait_log "workspace: view request for session rs-" 10
  check "the window left the Overview for a session's native view" wait_title " — work" 10
  check "the Overview keeps running behind it" vlive overview
  facts "app log for the overview" since
}

case_pager() {
  note "the scrollback pager is a utility view (W21)"
  ALPHA=$(cat $R/rs-alpha); PA=$(paneof $ALPHA)
  tell_tmux $PA "pager-marker-line"; tell_tmux $PA work; wait_snap "alpha .*icon=bolt" 10
  dact alpha; mark
  vkey dashboard e
  check "the workspace opened a pager view for alpha" wait_log "workspace: opening pager view pager:$ALPHA" 10
  check "the pager shows the end of alpha's scrollback" vwait pager "(END)" 10
  check "with the text the agent printed" vhas pager "pager-marker-line"
  check "the sidebar lists it under Workspace as a pager view" sh -c ". $here/env.sh; . $here/lib.sh; sidebar | grep -q 'alpha, pager view'"
  check "the pager attached nothing to the agent" test "$(clients | grep -c "pane=$PA ")" = 1
  check "the Dashboard was not suspended for it" vhas dashboard "> Sessions"
  vshot pager "alpha's scrollback in less"
  facts "sidebar" sidebar
  shot pager-view
  mark; vkey pager q
  check "quitting the pager ended its view" wait_log "the program of view pager:$ALPHA ended" 10
  check "and returned the pane to the Dashboard" waitfor 5 sh -c ". $here/env.sh; . $here/lib.sh; selected_row | grep -q 'Dashboard, dashboard view'"
  check "the pager row is gone from the sidebar" sh -c ". $here/env.sh; . $here/lib.sh; ! sidebar | grep -q 'pager view'"
}

case_shell() {
  note "a project shell is a utility view (W21, W19)"
  dact alpha; mark
  vkey dashboard o
  check "the workspace opened a shell view in the project directory" wait_log "workspace: opening shell view shell:$R/proj/alpha" 10
  check "the sidebar lists it under Workspace as a shell view" waitfor 5 sh -c ". $here/env.sh; . $here/lib.sh; sidebar | grep -q 'alpha, shell view'"
  sleep 1.5; vtype shell "pwd; echo SHELL_UTILITY_VIEW"; vkey shell Enter
  check "it is a shell" vwait shell "SHELL_UTILITY_VIEW" 10
  check "in the project directory" vhas shell "amxs14/proj/alpha"
  check "the Dashboard was not suspended for it" vhas dashboard "> Sessions"
  vshot shell "the shell view"
  shot shell-view
}

case_editor() {
  note "the editor is a utility view (W21, W19)"
  dact alpha; mark
  vkey dashboard O
  check "the workspace opened an editor view on the project directory" wait_log "workspace: opening editor view editor:$R/proj/alpha" 10
  check "it runs the configured editor on that path" vwait edit "editing: $R/proj/alpha" 10
  check "the sidebar lists it under Workspace as an editor view" sh -c ". $here/env.sh; . $here/lib.sh; sidebar | grep -q 'alpha, editor view'"
  check "the editor's environment carries no session or Kitty identity" vhas edit "identity variables in the editor's environment: 0"
  vshot edit "the editor view"
  facts "sidebar with the Dashboard, the Overview, a shell and an editor beside two sessions" sidebar
  shot editor-view
  mark; vkey edit Enter
  check "closing the editor ended its view" wait_log "the program of view editor:$R/proj/alpha ended" 10
  check "the editor row is gone from the sidebar" waitfor 5 sh -c ". $here/env.sh; . $here/lib.sh; ! sidebar | grep -q 'editor view'"
}

case_isolation() {
  note "utility processes are not managed agents: no borrowed identity, no session, no attention"
  ALPHA=$(cat $R/rs-alpha); PA=$(paneof $ALPHA); local PB=$(paneof $BETA)
  tell_tmux $PA work; tell_tmux $PB work; wait_snap "attention.total=0" 10
  local sessions_before=$(snapshot | grep -c ' runtime=tmux ') registry_before=$(registry | wc -l | tr -d ' ') panes_before=$(priv list-panes -a | wc -l | tr -d ' ')
  facts "before: snapshot" snapshot
  facts "before: registry" registry
  facts "what each view's terminal inherited from the app (shim log)" cat $R/logs/views.log
  check "the shell view's terminal inherited beta's runtime session and a Kitty window ID" grep -q "shell view: .*AMX_RUNTIME_SESSION_ID=$BETA KITTY_WINDOW_ID=999 CLAUDECODE=1" $R/logs/views.log

  dash
  # Back to the shell view left open by the shell case.
  check "the shell view is shown again, still running" showrow 'alpha, shell view' 
  vtype shell "echo IDENTITY_VARS=\$(env | grep -cE '^(KITTY_|AMX_RUNTIME_SESSION_ID=|TMUX|CLAUDECODE=)')"; vkey shell Enter
  check "the shell's environment carries none of them" vwait shell "IDENTITY_VARS=0" 10

  note "a harness started by hand in the shell view reports as nobody"
  vtype shell "claude"; vkey shell Enter
  check "the hand-started harness sees no runtime session and no Kitty window" vwait shell "runtime session: none  kitty window: none" 10
  vtype shell "ask"; vkey shell Enter
  check "it sent a PermissionRequest hook" vwait shell "asked (1)" 10
  sleep 2
  vshot shell "a harness run by hand in the shell view"
  check "no session was added to the daemon's list" test "$(snapshot | grep -c ' runtime=tmux ')" = "$sessions_before"
  check "nothing waits on anyone: attention total is still 0" snap_has "attention.total=0"
  check "beta, whose identity the app carried, is untouched" snap_has "beta .*attention=False"
  check "no runtime session was registered" test "$(registry | wc -l | tr -d ' ')" = "$registry_before"
  check "the private tmux server holds the same panes: a view is not on it" test "$(priv list-panes -a | wc -l | tr -d ' ')" = "$panes_before"
  check "the sidebar counts nothing waiting" sh -c ". $here/env.sh; . $here/lib.sh; ! sidebar | grep -q 'waiting'"
  facts "after: snapshot" snapshot
  facts "after: sidebar" sidebar
  vtype shell "quit"; vkey shell Enter; sleep 0.5

  note "control: the same hook from a process that does carry beta's identity reaches beta"
  local conv=$(priv capture-pane -p -S - -t $PB | sed -n 's/^conversation: //p' | head -1)
  printf '{"session_id":"%s","hook_event_name":"PermissionRequest","cwd":"/tmp","tool_name":"Bash"}' "$conv" \
    | AMX_RUNTIME_SESSION_ID=$BETA amx hook-indicator --harness claude PermissionRequest >/dev/null 2>&1
  check "with the identity, the hook marks beta as waiting" wait_snap "beta .*icon=key .*attention=True" 10
  facts "control: snapshot" snapshot
  tell_tmux $PB work; wait_snap "attention.total=0" 10

  note "the menu bar and the attention shortcut see sessions only"
  statusmenu_open; statusmenu_items > $R/menu.txt; menu_close
  facts "status menu" cat $R/menu.txt
  check "the status menu lists the two sessions" test "$(grep -v '^Mute' $R/menu.txt | grep -cE '(alpha|beta) · ')" = 2
  check "and no utility view" sh -c "! grep -qiE 'shell|dashboard view|overview view|pager|editor view' $R/menu.txt"
  tell_tmux $PA ask; wait_snap "alpha .*attention=True" 10
  showrow 'alpha, shell view'; mark
  hotkey 45
  check "the next-attention shortcut left the shell view for the waiting session" wait_title "alpha — work" 10
  check "and focused alpha's native view" wait_log "workspace: focus embedded $ALPHA" 10
  facts "app log for the shortcut" since
  tell_tmux $PA work; wait_snap "attention.total=0" 10

  note "a utility view ends with its program and goes back where it came from"
  showrow 'alpha, shell view'; mark
  vtype shell "exit"; vkey shell Enter
  check "exiting the shell ended its view" wait_log "the program of view shell:$R/proj/alpha ended" 10
  check "the shell row is gone" waitfor 5 sh -c ". $here/env.sh; . $here/lib.sh; ! sidebar | grep -q 'shell view'"
  check "both agents are still attached and running" sh -c ". $here/env.sh; . $here/lib.sh; test \$(clients | wc -l) -ge 2"
}

case_workflows() {
  note "every baseline workflow assigned to the desktop, reached from the embedded Dashboard with no Kitty"
  ALPHA=$(cat $R/rs-alpha); PA=$(paneof $ALPHA); local PB=$(paneof $BETA)

  note "W19 session actions: clear status, mark active, lock, mute"
  tell_tmux $PA ask; wait_snap "alpha .*attention=True" 10
  dact alpha; vkey dashboard x
  check "Clear status cleared alpha's attention" wait_snap "alpha .*attention=False" 10
  dact alpha; vkey dashboard b
  check "Mark active set alpha working" wait_snap "alpha .*icon=bolt" 10
  dact alpha; vkey dashboard L
  check "Lock shows on the row" vwait dashboard "🔒" 8
  dact alpha; vkey dashboard m; sleep 2
  facts "alpha's pane metadata after lock and mute" sh -c "tmux -S $R/tmp/agent-mux-$(id -u)/tmux.sock show-options -pv -t $PA @amx_vars | tr '_-' '/+' | base64 -d 2>/dev/null | tr ',' '\n' | grep -E 'locked|mute'"
  check "lock and mute are recorded on the tmux pane" sh -c "tmux -S $R/tmp/agent-mux-$(id -u)/tmux.sock show-options -pv -t $PA @amx_vars | tr '_-' '/+' | base64 -d 2>/dev/null | grep -q 'amx_locked\":\"true'"
  vshot dashboard "after clear, mark active, lock, mute"
  dact alpha; vkey dashboard L; sleep 1.5
  dact alpha; vkey dashboard m; sleep 1.5

  note "W21 keybinding reference; W10 a Kitty-only command explains itself; the Kitty tab pickers refuse"
  dash; vkey dashboard '?'
  check "the keyboard reference opens inside the Dashboard" vwait dashboard "close help" 8
  vshot dashboard "keyboard reference"; vkey dashboard Escape; sleep 0.5
  vkey dashboard :; sleep 0.5; vtype dashboard "resort"
  check "Resort Tabs says why it is unavailable" vwait dashboard "there are no Kitty tabs to reorder" 8
  vshot dashboard "palette: Resort Tabs unavailable"; vkey dashboard Escape; sleep 0.5
  facts "amx color-picker outside Kitty" sh -c "amx color-picker; echo exit=\$?"
  facts "amx icon-picker outside Kitty" sh -c "amx icon-picker; echo exit=\$?"
  check "the color picker refuses with a reason instead of needing Kitty" sh -c "amx color-picker 2>&1 | grep -q 'not Kitty'"

  note "W02 a fresh second instance of a running project"
  local before=$(count_live); dact alpha; mark; vkey dashboard C
  check "the dashboard asks which profile the new instance runs as" vwait dashboard "Launch new session with profile" 8
  vkey dashboard Enter
  check "a second alpha session is registered" waitfor 15 sh -c ". $here/env.sh; . $here/lib.sh; test \$(live | wc -l) = $((before+1))"
  local SECOND=$(newest)
  check "the workspace showed the new instance's native view" wait_log "workspace: attached a terminal to $SECOND" 20
  check "the first alpha session is untouched" attached_to $PA
  facts "registry" registry
  facts "sidebar" sidebar

  note "W09 close the second instance: its agent ends, its view goes, the others stay"
  local live_before="$(live)"
  dact alpha; mark; vkey dashboard X; sleep 1; vshot dashboard "after Close was asked"
  check "one runtime session ended" waitfor 15 sh -c ". $here/env.sh; . $here/lib.sh; test \$(live | wc -l) = $before"
  local CLOSED=$(comm -23 <(echo "$live_before" | sort) <(live | sort))
  say "closed: $CLOSED (the alpha row the dashboard had selected)"
  check "it is one of the two alpha sessions and is recorded ended" sh -c ". $here/env.sh; . $here/lib.sh; registry | grep -q '^$CLOSED ended .* alpha '"
  check "its pane is gone from the private server" test "$(priv list-panes -a | wc -l | tr -d ' ')" = "$before"
  check "the workspace let go of its terminal" waitfor 15 released "$CLOSED"
  ALPHA=$(registry | awk '$2=="live" && $4=="alpha"{print $1}' | tail -1); PA=$(paneof $ALPHA); echo "$ALPHA" > $R/rs-alpha
  check "the other alpha session is still attached" attached_to $PA
  check "beta is untouched" sh -c ". $here/env.sh; . $here/lib.sh; registry | grep -q '^$BETA live'"
  facts "registry" registry

  note "W08 restart: a new agent, a new runtime session, and its native view"
  dact alpha; mark; vkey dashboard r
  check "the registry records a restart whose parent is the old session" waitfor 20 sh -c ". $here/env.sh; . $here/lib.sh; registry | grep -q ' live restart alpha .*parent=$ALPHA'"
  local RESTARTED=$(registry | awk '$2=="live" && $3=="restart"{print $1}' | tail -1)
  check "the old runtime session is ended" sh -c ". $here/env.sh; . $here/lib.sh; registry | grep -q '^$ALPHA ended'"
  check "the workspace showed the restarted agent's native view" wait_log "workspace: attached a terminal to $RESTARTED" 20
  check "and let go of the old one's terminal" waitfor 15 released "$ALPHA"
  echo "$RESTARTED" > $R/rs-alpha; ALPHA=$RESTARTED; PA=$(paneof $ALPHA)
  facts "registry" registry
  facts "app log for the restart" since

  note "W22 Past Sessions; W04 resume a closed conversation; continuing it again focuses the live one"
  dash; vkey dashboard S
  check "Past Sessions lists the closed conversation" vwait dashboard "Past Sessions  [1 result]" 30
  vshot dashboard "Past Sessions in the embedded Dashboard"
  before=$(count_live); mark; vkey dashboard c
  check "the conversation was resumed as a new tmux session" waitfor 20 sh -c ". $here/env.sh; . $here/lib.sh; test \$(live | wc -l) = $((before+1))"
  local RESUMED=$(newest); local PR=$(paneof $RESUMED)
  check "the agent was started with --resume for that conversation" waitfor 10 pane_has $PR "conversation: 11111111-1111-4111-8111-111111111111"
  check "the registry binds the runtime session to the conversation" sh -c ". $here/env.sh; . $here/lib.sh; registry | grep -q '^$RESUMED live .*conv=11111111-1111-4111-8111-111111111111'"
  check "the workspace showed the resumed agent's native view" wait_log "workspace: attached a terminal to $RESUMED" 20
  facts "registry" registry
  wait_snap "$RESUMED" 10; sleep 2
  dash; mark; before=$(count_live); vkey dashboard c
  check "continuing it again asked the workspace for the live session" wait_log "workspace: view request for session $RESUMED" 15
  check "and resumed nothing twice" test "$(count_live)" = "$before"

  note "W05 native fork"
  dash; before=$(count_live); mark; vkey dashboard f
  check "the fork launched a new tmux session" waitfor 20 sh -c ". $here/env.sh; . $here/lib.sh; test \$(live | wc -l) = $((before+1))"
  local FORK=$(newest)
  check "started with --fork-session" waitfor 10 pane_has "$(paneof $FORK)" "--fork-session"
  check "the source session is untouched" sh -c ". $here/env.sh; . $here/lib.sh; registry | grep -q '^$RESUMED live'"
  check "the workspace showed the fork's native view" wait_log "workspace: attached a terminal to $FORK" 20

  note "W06 fork into an isolated worktree; W22 Worktrees view; reopen focuses the live session; shell view in the worktree"
  dash; vkey dashboard w
  check "the worktree fork asks for confirmation" vwait dashboard "Fork in new worktree" 8
  before=$(count_live); mark; vkey dashboard y
  check "the worktree fork launched a new tmux session" waitfor 25 sh -c ". $here/env.sh; . $here/lib.sh; test \$(live | wc -l) = $((before+1))"
  local WT=$(newest)
  check "in a managed worktree of the repository" sh -c "git -C $R/proj/alpha worktree list | grep -q 'agent-mux/worktrees'"
  check "the workspace showed its native view" wait_log "workspace: attached a terminal to $WT" 20
  facts "git worktrees" git -C $R/proj/alpha worktree list
  dash; vkey dashboard Escape; sleep 0.5; palette "worktrees"
  check "the Worktrees view lists it" vwait dashboard "Worktrees  [1/1 visible]" 15
  sleep 1.5; vshot dashboard "Worktrees view"
  wait_snap "$WT" 10; mark; before=$(count_live); vkey dashboard Enter
  check "reopen asked the workspace for the live worktree session" wait_log "workspace: view request for session $WT" 15
  check "and launched nothing" test "$(count_live)" = "$before"
  dash; mark; vkey dashboard o
  check "the worktree shell opened as a shell view" wait_log "workspace: opening shell view shell:" 10
  sleep 1.5; vtype shell "pwd; echo WORKTREE_SHELL_VIEW"; vkey shell Enter
  check "in the worktree" vwait shell "WORKTREE_SHELL_VIEW" 10
  vshot shell "shell view in the managed worktree"
  vtype shell "exit"; vkey shell Enter; sleep 1.5
  dash; vkey dashboard Escape; sleep 0.5

  note "W07 cross-profile handoff"
  dash; vkey dashboard S; vwait dashboard "Past Sessions  [" 30
  vkey dashboard H
  check "the handoff asks for the target profile" vwait dashboard "Handoff to profile" 8
  vshot dashboard "handoff: target picker"
  before=$(count_live); mark
  vkey dashboard j; sleep 0.4; vkey dashboard Enter
  check "the handoff launched its target on tmux" waitfor 25 sh -c ". $here/env.sh; . $here/lib.sh; test \$(live | wc -l) = $((before+1))"
  local HAND=$(newest)
  check "a handoff pack was written" sh -c "ls $HOME/.local/share/agent-mux/handoffs/*.md >/dev/null 2>&1"
  check "the target agent was told to read it" waitfor 10 pane_has "$(paneof $HAND)" "Read the handoff pack"
  check "under the other profile" wait_snap "alpha +personal .*$HAND" 15
  facts "handoff artifacts" sh -c "ls -R $HOME/.local/share/agent-mux/handoffs | head -20"
  facts "the handoff target's pane" priv capture-pane -p -t "$(paneof $HAND)"
  facts "registry" registry
  sleep 2; dash; vkey dashboard Escape; sleep 0.5
  check "the Dashboard lists the handoff target with the others" vwait dashboard "Running (" 8
  vshot dashboard "sessions after resume, fork, worktree fork and handoff"

  note "W22 Plans view and its editor, as an editor view"
  dash; palette "plans"
  check "the Plans view lists the saved plan" vwait dashboard "Plans  [1 result" 30
  vshot dashboard "Plans view"
  mark; vkey dashboard O
  check "the plan editor opened as an editor view" wait_log "workspace: opening editor view editor:$R/plans/demo" 10
  check "on the plan directory" vwait edit "editing: $R/plans/demo" 10
  vkey edit Enter; sleep 1.5
  dash; vkey dashboard Escape; sleep 0.5

  note "W27 ASL: a real asl run inside a tmux session shows on its row and in Runs; Enter on the run focuses the hosting session"
  perl -pi -e 's/\*\*Status\*\*: complete/**Status**: pending/' $R/plans/demo/session-1.md
  tell_tmux $PB asl
  check "the daemon marks beta as looping" wait_snap "beta .*looping=True" 30
  dash; vkey dashboard Escape; sleep 0.5
  vshot dashboard "dashboard while the run executes"
  palette "show runs"; sleep 3
  check "the Runs view shows the run, bound to beta" vwait dashboard "binding: beta" 15
  vshot dashboard "Runs view"
  mark; vkey dashboard Enter
  check "Enter on the run asked the workspace for the session hosting it" wait_log "workspace: view request for session $BETA" 15
  check "the window is on beta's native view" wait_title "beta — work" 10
  facts "asl run log (tail)" tail -5 $R/logs/aslrun.log
  facts "snapshot" snapshot
  dash; vkey dashboard Escape; sleep 0.5
  shot workflows-end
  facts "sidebar at the end" sidebar
  facts "registry at the end" registry
}

case_nokitty() {
  note "nothing in this run used Kitty"
  facts "daemon health" health
  facts "world processes and whether any was given a Kitty socket" sh -c "ps eww -axo pid,command | grep -E '/tmp/amxs14/(bin|shim|app)' | grep -v grep | sed -E 's/^( *[0-9]+ [^ ]+( [a-z-]+){0,3}).*/\1/' | sort | uniq -c | sort -rn | head -20"
  check "the daemon never had a Kitty" sh -c ". $here/env.sh; . $here/lib.sh; health | grep -q 'kitty_snapshot.ready=False'"
  check "no process of the world carries KITTY_LISTEN_ON" sh -c "! ps eww -axo command | grep -E '^/tmp/amxs14/(bin|shim)/' | grep -q 'KITTY_LISTEN_ON='"
  check "the app never asked for a Kitty window to be focused" sh -c ". $here/env.sh; . $here/lib.sh; ! applog | grep -qiE 'focus Kitty window|dashboard (opened|focused)'"
  check "every agent of the run lives on the private tmux server" sh -c ". $here/env.sh; . $here/lib.sh; ! registry | awk '{print \$5}' | grep -q kitty"
  check "the Dashboard and the Overview are still running in the app" sh -c ". $here/env.sh; . $here/lib.sh; vlive dashboard && vlive overview"
  facts "views started during the run (shim log)" cat $R/logs/views.log
  facts "app log, whole run" applog
}

for c in "$@"; do "case_$c"; done
printf '\n##### RESULT: %d check(s) failed [%s]\n' "$fail" "$*" >> "$LOG"
echo "failed checks: $fail"

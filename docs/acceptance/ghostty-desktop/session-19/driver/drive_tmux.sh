#!/bin/bash
# Runs on the MacBook, headless. Drives the AgentMux dashboard and overview on
# the tmux backend with no Kitty anywhere in their environment, through every
# portable workflow, and records what was on screen.
#
# The surface under test runs in a window of an observer tmux server (its own
# socket and no configuration): only a screen recorder and a keyboard. It is
# not AgentMux's private server and hosts no agent. Nothing is displayed.
#
# The later steps (W13 to W16) act on one agent without the dashboard: they
# type at the agent through the private server (send-keys attaches nothing)
# and read what the daemon holds for it.
# usage: drive_tmux.sh LOG
R=/tmp/amxs19; . $R/driver/remote-env.sh
LOG=$1; T=dash; : > "$LOG"
note() { printf '\n##### %s\n' "$*" >> "$LOG"; echo "## $*"; }
shot() { printf '\n=== %s [%s] ===\n' "$1" "$(date -u +%H:%M:%SZ)" >> "$LOG"; obs capture-pane -p -t $T | sed -e 's/[[:space:]]*$//' | cat -s >> "$LOG"; }
k() { obs send-keys -t $T "$@"; }
typ() { obs send-keys -t $T -l "$1"; }
waitfor() { # text [seconds]
  local n=$(( ${2:-15} * 4 ))
  while [ $n -gt 0 ]; do obs capture-pane -p -t $T | grep -qF -- "$1" && { printf 'PASS: on screen: %s\n' "$1" >> "$LOG"; return 0; }; sleep 0.25; n=$((n-1)); done
  printf '\nFAIL: TIMEOUT waiting for: %s\n' "$1" >> "$LOG"; echo "TIMEOUT: $1"; shot "screen at timeout"; return 1
}
waitgone() { # text [seconds]: the surface no longer shows it
  local n=$(( ${2:-15} * 4 ))
  while [ $n -gt 0 ]; do obs capture-pane -p -t $T | grep -qF -- "$1" || { printf 'PASS: no longer on screen: %s\n' "$1" >> "$LOG"; return 0; }; sleep 0.25; n=$((n-1)); done
  printf '\nFAIL: still on screen: %s\n' "$1" >> "$LOG"; echo "STILL: $1"; shot "screen at timeout"; return 1
}
fail=0; passed=0; step() { if "$@"; then passed=$((passed+1)); else fail=$((fail+1)); fi; }
# check WHAT COMMAND...: a condition on state read from the world.
check() { local what=$1; shift; if "$@" >/dev/null 2>&1; then printf 'PASS: %s\n' "$what" >> "$LOG"; passed=$((passed+1)); else printf 'FAIL: %s\n' "$what" >> "$LOG"; echo "FAIL: $what"; fail=$((fail+1)); fi; }
ev() { eval "$1"; }
facts() { printf '\n--- %s ---\n' "$1" >> "$LOG"; shift; "$@" >> "$LOG" 2>&1; }
surface_env() { obs show-environment -g | grep -E '^(HOME|TMPDIR|PATH|TERM|KITTY|ZDOTDIR)'; echo "KITTY variables: $(obs show-environment -g | grep -c '^KITTY')"; }
panes() { priv list-panes -a -F '#{session_name} #{pane_id} pid=#{pane_pid} dead=#{pane_dead} #{pane_current_path}'; }

obs kill-server 2>/dev/null
obs new-session -d -s $T -x 140 -y 40 "amx; echo SURFACE_EXIT=\$?; exec sleep 3600"

note "environment"
facts "the Mac" sh -c 'scutil --get ComputerName; sw_vers -productVersion; uname -m'
facts "the surface's environment: no Kitty variable" surface_env
facts "amx build (the packaged amx)" amx --version
facts "amx backend status" amx backend status
check "no Kitty process runs in this world" ev '! pgrep -f "kitty.*$R/" '
check "the private tmux server does not exist before the first launch" ev '! priv list-panes -a'

note "W1 dashboard starts outside Kitty with no sessions"
step waitfor "No running sessions"; shot "empty dashboard"

note "W2 launch a configured project on the tmux backend from the launcher"
k l; step waitfor "Launch Sessions"; shot "launcher"
k Space; sleep 0.3; k Enter
step waitfor "Launched 1/1"; step waitfor "stand-in claude pid"; shot "dashboard after launch: row + live inspector preview"
facts "private tmux server after launch" panes
facts "runtime-session registry" registry
check "one agent pane runs on the private server" ev '[ "$(panes | grep -c "dead=0")" = 1 ]'
check "the registry holds one live tmux session for alpha" ev '[ "$(registry | grep -c " live .* tmux .*project=alpha")" = 1 ]'
check "the server runs the packaged tmux, from the copy outside the app" ev 'ps -o command= -p "$(priv display-message -p "#{pid}")" | grep -q "$DATA/runtime/tmux-"'

note "W3 focus = attach this terminal to the session; hook state; F12 detaches back to the dashboard"
k Enter; step waitfor "conversation: standin-"; shot "attached view of the agent pane"
facts "private server clients while attached" priv list-clients -F '#{client_session} #{client_termname}'
check "one terminal is attached to the private server" ev '[ "$(priv list-clients | wc -l | tr -d " ")" = 1 ]'
typ "ask"; k Enter; step waitfor "asked (1)"; shot "attached: agent emitted a PermissionRequest hook"
k F12; step waitfor "Needs permission"; shot "back on the dashboard: Needs you / Needs permission from the managed hook"
check "no terminal is attached after F12" ev '[ "$(priv list-clients | wc -l | tr -d " ")" = 0 ]'
check "the agent kept running through attach and detach" ev '[ "$(panes | grep -c "dead=0")" = 1 ]'

note "W4 session actions on the tmux row: clear, mark active, lock, mute, peek, shell and editor utility views"
k a; step waitfor "Actions for alpha"; shot "actions menu for a tmux session"
k x; step waitfor "requested status clear"; sleep 1.5; shot "after Clear status"
k a; step waitfor "Actions for alpha"; k b; step waitfor "Working"; shot "after Mark active"
k a; step waitfor "Actions for alpha"; k L; step waitfor "🔒"; shot "after Lock"
k a; step waitfor "Actions for alpha"; k m; sleep 2; shot "after Mute"
facts "pane metadata after lock+mute" sh -c "\"$TM\" -S $TMPDIR/agent-mux-$(id -u)/tmux.sock show-options -pv -t %0 @amx_vars | tr '_-' '/+' | base64 -d 2>/dev/null | tr ',' '\n' | grep -E 'locked|mute'"
check "the pane's metadata records the lock and the mute" ev '[ "$(priv show-options -pv -t %0 @amx_vars | tr "_-" "/+" | base64 -d 2>/dev/null | tr "," "\n" | grep -cE "amx_locked.*true|cc_mute.*1")" = 2 ]'
k a; step waitfor "Actions for alpha"; k e; step waitfor "(END)"; shot "Peek scrollback: less utility view in this terminal"
k q; step waitfor "Sessions"
k a; step waitfor "Actions for alpha"; k L; sleep 2
k a; step waitfor "Actions for alpha"; k m; sleep 2
k a; step waitfor "Actions for alpha"; k o; sleep 2; typ "pwd; echo SHELL_UTILITY_VIEW"; k Enter; step waitfor "SHELL_UTILITY_VIEW"; shot "Open shell: utility view in this terminal"
typ "exit"; k Enter; step waitfor "Sessions"
k a; step waitfor "Actions for alpha"; k O; step waitfor "EDITOR VIEW"; shot "Open editor: utility view in this terminal"
k Enter; step waitfor "Sessions"; shot "dashboard resumed after the utility views"

note "W5 capability-aware command: Resort Tabs explains itself without Kitty"
k :; sleep 0.4; typ "resort"; step waitfor "there are no Kitty tabs to reorder"; shot "palette: Resort Tabs unavailable with reason"
k Escape; sleep 0.5

note "W6 restart on the recorded backend (new runtime session, old one ended)"
k a; step waitfor "Actions for alpha"; k r; step waitfor "restarted alpha" 20; sleep 2; shot "after Restart"
facts "registry after restart" registry
check "the restart is a new live runtime session and the old one is ended" ev '[ "$(registry | grep -c " live restart ")" = 1 ] && [ "$(registry | grep -c " ended ")" = 1 ]'
check "exactly one agent runs for alpha" ev '[ "$(panes | grep -c "dead=0")" = 1 ]'

note "W7 history and continuity: Past Sessions lists the conversation; continue resumes it on tmux; continuing again attaches to the live session"
k S; step waitfor "Past Sessions  [1 result]" 30; shot "Past Sessions outside Kitty"
k c; sleep 5
facts "private tmux server after continue" panes
check "the resumed conversation runs in a second pane" ev '[ "$(panes | grep -c "dead=0")" = 2 ]'
check "it was resumed with its own conversation ID" ev 'registry | grep -q " live .*conv=11111111-1111-4111-8111-111111111111"'
k c; step waitfor "conversation: 11111111-1111-4111-8111-111111111111"; shot "continue on a conversation live in tmux: attached to it instead of resuming twice"
check "continuing a live conversation created no new pane" ev '[ "$(panes | grep -c "dead=0")" = 2 ]'
k F12; step waitfor "Past Sessions"

note "W8 worktrees: fork the conversation into a managed worktree on tmux; Worktrees view; reopen attaches; shell view"
k w; step waitfor "Fork in new worktree"; shot "worktree fork confirmation"
k y; sleep 6
facts "private tmux server after worktree fork" panes
check "the fork runs in a third pane, inside a managed worktree" ev '[ "$(panes | grep -c "dead=0")" = 3 ] && panes | grep -q "agent-mux/worktrees/"'
k Escape; sleep 0.5; k :; sleep 0.4; typ "worktrees"; sleep 0.5; k Enter; step waitfor "Worktrees  [1/1 visible]"; sleep 1.5; shot "Worktrees view outside Kitty: live managed worktree"
k Enter; step waitfor "--fork-session"; shot "worktree reopen: attached to the live worktree session"
k F12; step waitfor "Worktrees  ["
k o; sleep 2; typ "pwd; echo WORKTREE_SHELL_VIEW"; k Enter; step waitfor "WORKTREE_SHELL_VIEW"; shot "worktree shell utility view"
typ "exit"; k Enter; step waitfor "Worktrees  ["
k Escape; sleep 0.5

note "W9 plans: Plans view lists the plan; its editor opens in this terminal"
k :; sleep 0.4; typ "plans"; sleep 0.5; k Enter; step waitfor "demo-plan" 30; shot "Plans view outside Kitty"
k O; step waitfor "EDITOR VIEW"; shot "plan editor utility view"
k Enter; step waitfor "Plans  ["; k Escape; sleep 0.5

note "W10 overview on the tmux backend: tiles, previews read from the panes, attach and back; no viewer is left behind"
k Escape; sleep 0.3; step waitfor "Sessions"
obs new-window -d -n ov "amx overview; echo OVERVIEW_EXIT=\$?; exec sleep 3600"
T=ov
step waitfor "Live sessions  3"; shot "overview: three tmux sessions"
check "reading the sessions for the overview attached no terminal" ev '[ "$(priv list-clients | wc -l | tr -d " ")" = 0 ]'
k c; sleep 2.5; step waitfor "stand-in claude pid"; shot "overview: previews captured from the panes"
check "previews acquired no input or resize ownership: still no client" ev '[ "$(priv list-clients | wc -l | tr -d " ")" = 0 ]'
k Enter; step waitgone "Live sessions"; step waitfor "conversation: "; shot "overview: attached to the selected session"
check "the overview's Enter attached one terminal" ev '[ "$(priv list-clients | wc -l | tr -d " ")" = 1 ]'
k F12; step waitfor "Live sessions"; sleep 1; k q; step waitfor "OVERVIEW_EXIT=0"
check "no terminal is attached after leaving the overview" ev '[ "$(priv list-clients | wc -l | tr -d " ")" = 0 ]'
T=dash

note "W11 the pickers are portable views: they open in this terminal and edit a tmux session"
rid=$(registry | awk '/ live restart /{print $1}'); pane=$(registry | awk '/ live restart /{print $5}')
facts "the session the next steps act on (the restarted alpha)" echo "$rid $pane"
check "one restarted session to act on" ev '[ -n "$rid" ] && [ -n "$pane" ]'
unb64() { python3 -c 'import base64,sys; s=sys.stdin.read().strip(); sys.stdout.write(base64.urlsafe_b64decode(s+"="*(-len(s)%4)).decode())'; }
title_of() { priv display-message -p -t "$pane" "#{@amx_title}" | unb64; echo; }
color_of() { priv show-options -pv -t "$pane" @amx_vars | unb64 | python3 -c 'import json,sys; print("cc_color=" + json.load(sys.stdin).get("cc_color","(none)"))'; }
picker_unnamed() { amx icon-picker; echo "exit status: $?"; }
facts "amx icon-picker with three sessions and none named" picker_unnamed
check "with several sessions and none named the picker lists them and opens nothing" ev '! amx icon-picker && amx icon-picker 2>&1 | grep -q "3 tmux sessions are running"'
facts "title of the session before" title_of
facts "color of the session before" color_of
obs new-window -d -n pick "amx icon-picker --runtime-session $rid; echo ICON_EXIT=\$?; exec sleep 3600"
T=pick
step waitfor "Set Harness Icon"; shot "icon picker, in this terminal, for a tmux session"
k j; sleep 0.3; k Enter; step waitfor "ICON_EXIT=0"
facts "title of the session after 'None (clear icon)'" title_of
check "the icon picker wrote the session's title: the harness icon is gone" ev '[ "$(title_of)" = alpha ]'
obs kill-window -t pick
obs new-window -d -n pick "amx color-picker --runtime-session $rid; echo COLOR_EXIT=\$?; exec sleep 3600"
step waitfor "Set Color: alpha"; shot "color picker, in this terminal, for a tmux session"
k j; sleep 0.3; k Enter; step waitfor "COLOR_EXIT=0"
facts "color of the session after the pick" color_of
check "the color picker recorded the pick on the session (red, the one after the profile's blue)" ev '[ "$(color_of)" = cc_color=red ]'
obs kill-window -t pick
T=dash

# dstate: the daemon's state for the session: its icon and whether it is unseen.
dstate() { python3 - "$rid" <<'PY'
import json,socket,os,sys
s=socket.socket(socket.AF_UNIX);s.connect(os.path.expanduser('~/.local/share/agent-mux/daemon.sock'));s.sendall(b'{"type":"state_query"}\n')
buf=b''
while not buf.endswith(b'\n'):
    c=s.recv(65536)
    if not c: break
    buf+=c
d=json.loads(buf)
for slot,b in d.get('runtime_slots',{}).items():
    if b['runtime_session_id']==sys.argv[1]:
        st=d['states'].get(slot,{})
        print('icon=%s unseen=%s' % (st.get('icon_state') or '-', bool(st.get('unseen'))))
PY
}
until_state() { # PATTERN SECONDS: waits for the daemon's state of the session to match
  local n=$(( $2 * 2 ))
  while [ $n -gt 0 ]; do dstate | grep -q -- "$1" && { printf 'PASS: daemon state: %s\n' "$1" >> "$LOG"; return 0; }; sleep 0.5; n=$((n-1)); done
  printf 'FAIL: TIMEOUT waiting for daemon state: %s (it is: %s)\n' "$1" "$(dstate)" >> "$LOG"; echo "TIMEOUT state: $1"; return 1
}
say() { priv send-keys -t "$pane" -l "$1"; priv send-keys -t "$pane" Enter; }
# exported WHAT...: the export of the session, summarized.
summarize() { python3 -c '
import json,sys
d=json.load(sys.stdin)
for w in d["windows"]:
    stops=[e for e in w["events"] if e.get("event")=="Stop"]
    print("runtime_session_id=%s backend=%s events=%d live.found=%s last_stop_confirmed_by=%s" % (w.get("runtime_session_id"), w.get("backend"), len(w["events"]), w["live"]["found"], stops[-1].get("stop_confirmed_by") if stops else None))
print("windows=%d" % len(d["windows"]))'; }
export_current() { AMX_RUNTIME_SESSION_ID=$rid KITTY_WINDOW_ID=999 amx daemon export-events --current-window --pretty=false | summarize; }
export_named() { amx daemon export-events --runtime-session "$rid" --pretty=false | summarize; }
export_outside() { amx daemon export-events --current-window; echo "exit status: $?"; }

note "W13 event export selected by runtime session, as typed in a tmux session"
facts "amx daemon export-events --current-window, with AMX_RUNTIME_SESSION_ID and a stale KITTY_WINDOW_ID=999" export_current
check "--current-window in a tmux session exports that session alone, by its runtime session" ev 'export_current | grep -q "^runtime_session_id=$rid backend=tmux events=[1-9].* live.found=False" && export_current | grep -qx "windows=1"'
check "--runtime-session selects the same session from anywhere" ev 'export_named | grep -q "^runtime_session_id=$rid backend=tmux" && export_named | grep -qx "windows=1"'
facts "amx daemon export-events --current-window outside any session" export_outside
check "outside any session --current-window is refused" ev '! amx daemon export-events --current-window'

note "W14 stop hold and focus observation on tmux: a stop nothing confirms is released at the deadline; attaching a terminal marks the session seen"
say work; step until_state "icon=bolt" 10
facts "state while working" dstate
t0=$(date +%s); say done
step until_state "icon=stop" 40; t1=$(date +%s)
facts "state at rest" dstate
facts "how long the stop was held" echo "$((t1-t0)) s"
check "the stop was held for confirmation and released at the deadline, not at once (held at least 10 s)" ev '[ $((t1-t0)) -ge 10 ]'
facts "the export of the session" export_named
check "the export records the stop as released by the deadline" ev 'export_named | grep -q "last_stop_confirmed_by=deadline"'
check "at rest with no terminal attached the session is unseen" ev 'dstate | grep -q "icon=stop unseen=True"'
check "no terminal is attached" ev '[ "$(priv list-clients | wc -l | tr -d " ")" = 0 ]'
sleep 5
check "still unseen after two more maintenance passes with nobody looking" ev 'dstate | grep -q "unseen=True"'
obs new-window -d -n att "amx attach $rid; echo ATTACH_EXIT=\$?; exec sleep 3600"
step until_state "icon=stop unseen=False" 12
facts "state after a terminal attached" dstate
check "one terminal is attached, and the agent is the same pane" ev '[ "$(priv list-clients | wc -l | tr -d " ")" = 1 ] && panes | grep -q " $pane .*dead=0"'
obs kill-window -t att; sleep 1
check "no terminal is attached after the view closed" ev '[ "$(priv list-clients | wc -l | tr -d " ")" = 0 ]'

note "W15 context limit on tmux: the banner on a working agent's pane is read with no terminal attached"
say work; step until_state "icon=bolt" 10
say limit; step until_state "icon=error" 15
facts "state with the banner on the pane" dstate
facts "the pane, as the daemon read it" priv capture-pane -p -t "$pane"
check "reading the pane attached no terminal" ev '[ "$(priv list-clients | wc -l | tr -d " ")" = 0 ]'
capture_health() { amx doctor --json | python3 -c 'import json,sys; c=json.load(sys.stdin)["daemon"]["runtime"]["capture"]; print(json.dumps(c)); sys.exit(0 if c.get("reads",0)>0 and c.get("consecutive_failures",0)==0 else 1)'; }
facts "amx doctor --json: the daemon's pane reads" capture_health
check "the daemon's health counts its pane reads, with none failing now" capture_health

note "W12 close ends the session and its runtime record; the dashboard quits cleanly"
step waitfor "Sessions"
before=$(panes | grep -c "dead=0")
k a; sleep 1; shot "actions menu before close"; k X; sleep 3.5; shot "after Close"
facts "registry at the end" registry
check "closing ended exactly one agent" ev '[ "$(panes | grep -c "dead=0")" = $((before-1)) ]'
k q; step waitfor "SURFACE_EXIT=0" 8; shot "after q: the surface exited"
facts "daemon state: runtime slots, and no Kitty" python3 -c "
import json,socket,os
s=socket.socket(socket.AF_UNIX);s.connect(os.path.expanduser('~/.local/share/agent-mux/daemon.sock'));s.sendall(b'{\"type\":\"state_query\"}\n')
buf=b''
while not buf.endswith(b'\n'):
    c=s.recv(65536)
    if not c: break
    buf+=c
d=json.loads(buf)
for slot,b in sorted(d.get('runtime_slots',{}).items()):
    st=d['states'].get(slot,{})
    print('slot',slot,b['backend'],b['runtime_session_id'],'icon='+repr(st.get('icon_state')))
print('kitty_snapshot.ready=',(d.get('kitty_snapshot') or {}).get('ready'))"

note "W16 a tmux server that disappears under running agents does not empty the list"
left=$(panes | grep -c "dead=0")
obs new-window -d -n ov2 "amx overview; echo OVERVIEW_EXIT=\$?; exec sleep 3600"
T=ov2
step waitfor "Live sessions  $left"; shot "overview before the server is killed"
spid=$(priv display-message -p "#{pid}")
facts "kill -9 of the private tmux server" sh -c "kill -9 $spid; echo killed pid $spid"
step waitfor "tmux outage: the tmux server is gone" 15; shot "overview after the server vanished: tiles kept, outage named"
check "the sessions it took are still listed" ev 'obs capture-pane -p -t ov2 | grep -q "Live sessions  $left"'
sleep 5
check "and stay listed on later refreshes" ev 'obs capture-pane -p -t ov2 | grep -q "Live sessions  $left" && obs capture-pane -p -t ov2 | grep -q "tmux outage"'
facts "registry after the server vanished" registry
check "the daemon recorded them as stopped, and nothing was relaunched" ev '[ "$(registry | grep -c " live ")" = 0 ] && ! priv list-panes -a'
k q; step waitfor "OVERVIEW_EXIT=0"
T=dash

note "teardown of this run: the agents are ended through amx, the daemon stopped"
facts "amx backend stop --yes" amx backend stop --yes
facts "amx daemon stop" amx daemon stop
check "no private tmux server is left" ev '! priv list-panes -a'
obs kill-server 2>/dev/null
rm -rf $DATA/runtime-sessions.json $HOME/.local/state $HOME/.local/share/agent-mux/worktrees $HOME/.claude-a/projects/-private-tmp-amxs19-home* 2>/dev/null
git -C $R/proj/alpha worktree prune 2>/dev/null
for b in $(git -C $R/proj/alpha branch --list 'amx/*' --format='%(refname:short)'); do git -C $R/proj/alpha branch -q -D "$b"; done
printf '\n##### RESULT: %d passed, %d failed\n' "$passed" "$fail" >> "$LOG"; echo "tmux: $passed passed, $fail failed"

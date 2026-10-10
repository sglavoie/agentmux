#!/bin/bash
# Runs on the MacBook, headless. Drives the AgentMux dashboard and overview on
# the tmux backend with no Kitty anywhere in their environment, through every
# portable workflow, and records what was on screen.
#
# The surface under test runs in a window of an observer tmux server (its own
# socket and no configuration): only a screen recorder and a keyboard. It is
# not AgentMux's private server and hosts no agent. Nothing is displayed.
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

note "W11 Kitty-only commands refuse outside Kitty and say why"
facts "amx color-picker" sh -c 'amx color-picker; echo "exit status: $?"'
facts "amx icon-picker" sh -c 'amx icon-picker; echo "exit status: $?"'
check "color-picker refuses with a nonzero status" ev '! amx color-picker'
check "icon-picker refuses with a nonzero status" ev '! amx icon-picker'

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

note "teardown of this run: the agents are ended through amx, the daemon stopped"
facts "amx backend stop --yes" amx backend stop --yes
facts "amx daemon stop" amx daemon stop
check "no private tmux server is left" ev '! priv list-panes -a'
obs kill-server 2>/dev/null
rm -rf $DATA/runtime-sessions.json $HOME/.local/state $HOME/.local/share/agent-mux/worktrees $HOME/.claude-a/projects/-private-tmp-amxs19-home* 2>/dev/null
git -C $R/proj/alpha worktree prune 2>/dev/null
for b in $(git -C $R/proj/alpha branch --list 'amx/*' --format='%(refname:short)'); do git -C $R/proj/alpha branch -q -D "$b"; done
printf '\n##### RESULT: %d passed, %d failed\n' "$passed" "$fail" >> "$LOG"; echo "tmux: $passed passed, $fail failed"

#!/bin/bash
# Drives the AgentMux dashboard and overview, running WITHOUT Kitty on the tmux
# backend, through every portable workflow, and records what was on screen.
# The surface under test runs in window "dash" of an observer tmux server
# (its own socket; only a recorder and keyboard). usage: drive_portable.sh LOG
R=/tmp/amxs11; o=$R/obs.sh; LOG=$1; T=dash
. $R/env.sh
priv() { tmux -S $R/tmp/agent-mux-$(id -u)/tmux.sock "$@"; }
note() { printf '\n##### %s\n' "$*" >> "$LOG"; echo "## $*"; }
shot() { printf '\n=== %s [%s] ===\n' "$1" "$(date -u +%H:%M:%SZ)" >> "$LOG"; $o capture-pane -p -t $T | sed -e 's/[[:space:]]*$//' | cat -s >> "$LOG"; }
k() { $o send-keys -t $T "$@"; }
typ() { $o send-keys -t $T -l "$1"; }
waitfor() { # text [seconds]
  local n=$(( ${2:-15} * 4 ))
  while [ $n -gt 0 ]; do $o capture-pane -p -t $T | grep -qF -- "$1" && return 0; sleep 0.25; n=$((n-1)); done
  printf '\n!!! TIMEOUT waiting for: %s\n' "$1" >> "$LOG"; echo "TIMEOUT: $1"; shot "screen at timeout"; return 1
}
fail=0; step() { "$@" || fail=$((fail+1)); }
facts() { printf '\n--- %s ---\n' "$1" >> "$LOG"; shift; "$@" >> "$LOG" 2>&1; }

note "environment"
facts "host terminal of the observer client" $o list-clients -F 'client_termname=#{client_termname} client_tty=#{client_tty} size=#{client_width}x#{client_height}'
facts "surface environment (no Kitty)" $o show-environment -g KITTY_LISTEN_ON
facts "amx build" amx --version
facts "private tmux server before launch (expect none)" priv list-panes -a

note "W1 dashboard starts outside Kitty with no sessions"
step waitfor "No running sessions"; shot "empty dashboard"

note "W2 launch a configured project on the tmux backend from the launcher"
k l; step waitfor "Launch Sessions"; shot "launcher"
k Space; sleep 0.3; k Enter
step waitfor "Launched 1/1"; step waitfor "stand-in claude pid"; shot "dashboard after launch: row + live inspector preview"
facts "private tmux server after launch" priv list-panes -a -F '#{session_name} #{pane_id} pid=#{pane_pid} dead=#{pane_dead} #{pane_current_path}'
facts "runtime-session registry" python3 -c "
import json;d=json.load(open('$R/home/.local/share/agent-mux/runtime-sessions.json'))
for s in d['sessions']: print(s['id'],s['state'],s['relation'],s['target']['instance']['kind'],s['target']['ref'],'conv='+str((s.get('conversation') or {}).get('session_id')))"

note "W3 focus = attach this terminal to the session; hook state; F12 detaches back to the dashboard"
k Enter; step waitfor "conversation: standin-"; shot "attached view of the agent pane"
facts "private server clients while attached" priv list-clients -F '#{client_session} #{client_termname}'
typ "ask"; k Enter; step waitfor "asked (1)"; shot "attached: agent emitted a PermissionRequest hook"
k F12; step waitfor "Needs permission"; shot "back on the dashboard: Needs you / Needs permission from the managed hook"
facts "private server clients after detach (expect none)" priv list-clients -F '#{client_session}'

note "W4 session actions on the tmux row: clear, mark active, lock, mute, peek (pager utility view), shell + editor utility views"
k a; step waitfor "Actions for alpha"; shot "actions menu for a tmux session"
k x; step waitfor "requested status clear"; sleep 1.5; shot "after Clear status"
k a; step waitfor "Actions for alpha"; k b; step waitfor "Working"; shot "after Mark active"
k a; step waitfor "Actions for alpha"; k L; step waitfor "🔒"; shot "after Lock"
k a; step waitfor "Actions for alpha"; k m; sleep 2; shot "after Mute"
facts "pane metadata after lock+mute" sh -c "tmux -S $R/tmp/agent-mux-$(id -u)/tmux.sock show-options -pv -t %0 @amx_vars | tr '_-' '/+' | base64 -d 2>/dev/null | tr ',' '\n' | grep -E 'locked|mute'"
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
facts "registry after restart" python3 -c "
import json;d=json.load(open('$R/home/.local/share/agent-mux/runtime-sessions.json'))
for s in d['sessions']: print(s['id'],s['state'],s['relation'],'parent='+str(s.get('parent')))"

note "W7 history + continuity: Past Sessions lists the conversation; continue resumes it on tmux; continuing it again attaches to the live session"
k S; step waitfor "Past Sessions  [1 result]" 30; shot "Past Sessions outside Kitty"
k c; sleep 5
facts "private tmux server after continue" priv list-panes -a -F '#{session_name} #{pane_id} pid=#{pane_pid} dead=#{pane_dead}'
facts "resumed pane screen" priv capture-pane -p -t %2
k c; step waitfor "conversation: 11111111-1111-4111-8111-111111111111"; shot "continue on a conversation live in tmux: attached to it instead of resuming twice"
facts "panes after second continue (expect no new pane)" priv list-panes -a -F '#{session_name} #{pane_id} pid=#{pane_pid}'
k F12; step waitfor "Past Sessions"

note "W8 worktrees: fork the conversation into a managed worktree on tmux; Worktrees view; reopen attaches; shell view"
k w; step waitfor "Fork in new worktree"; shot "worktree fork confirmation"
k y; sleep 6
facts "private tmux server after worktree fork" priv list-panes -a -F '#{session_name} #{pane_id} pid=#{pane_pid} #{pane_current_path}'
k Escape; sleep 0.5; k :; sleep 0.4; typ "worktrees"; sleep 0.5; k Enter; step waitfor "Worktrees  [1/1 visible]"; sleep 1.5; shot "Worktrees view outside Kitty: live managed worktree"
k Enter; step waitfor "--fork-session"; shot "worktree reopen: attached to the live worktree session"
k F12; step waitfor "Worktrees  ["
k o; sleep 2; typ "pwd; echo WORKTREE_SHELL_VIEW"; k Enter; step waitfor "WORKTREE_SHELL_VIEW"; shot "worktree shell utility view"
typ "exit"; k Enter; step waitfor "Worktrees  ["
k Escape; sleep 0.5

note "W9 plans: Plans view lists the plan; editor opens in this terminal"
k :; sleep 0.4; typ "plans"; sleep 0.5; k Enter; step waitfor "demo-plan" 30; shot "Plans view outside Kitty"
k O; step waitfor "EDITOR VIEW"; shot "plan editor utility view"
k Enter; step waitfor "Plans  ["; k Escape; sleep 0.5

note "W10 ASL: a real asl run inside a tmux session shows on its row and in Runs, bound without Kitty"
k Escape; sleep 0.3; step waitfor "Sessions"
shot "dashboard before the ASL run (three tmux sessions)"
priv send-keys -t %1 -l "asl"; priv send-keys -t %1 Enter
facts "typed 'asl' into pane %1 (the restarted alpha session) through the private server" echo ok
sleep 14
shot "dashboard while the run executes: loop marker on the session hosting it"
k :; sleep 0.4; typ "show runs"; sleep 0.5; k Enter; sleep 3; shot "Runs view outside Kitty while the run is live"
k Enter; sleep 2.5; shot "enter on the run: attached to the tmux session hosting it"
k F12; sleep 2; k Escape; sleep 0.5
facts "daemon state query: runtime slots and ASL runs" python3 -c "
import json,socket
s=socket.socket(socket.AF_UNIX);s.connect('$R/home/.local/share/agent-mux/daemon.sock');s.sendall(b'{\"type\":\"state_query\"}\n')
buf=b''
while not buf.endswith(b'\n'):
    c=s.recv(65536)
    if not c: break
    buf+=c
d=json.loads(buf)
for slot,b in sorted(d.get('runtime_slots',{}).items()):
    st=d['states'].get(slot,{})
    print('slot',slot,b['backend'],b['runtime_session_id'],'icon='+repr(st.get('icon_state')),'looping=',st.get('looping'),'asl_run_ids=',st.get('asl_run_ids'),'asl_phases=',st.get('asl_phases'))
print('kitty_snapshot.ready=',(d.get('kitty_snapshot') or {}).get('ready'))"
facts "asl run log (tail)" tail -5 $R/aslrun.log

note "W11 close ends the session and its runtime record; dashboard quits cleanly"
step waitfor "Sessions"
k a; sleep 1; shot "actions menu before close"; k X; sleep 3; shot "after Close"
facts "registry at the end" python3 -c "
import json;d=json.load(open('$R/home/.local/share/agent-mux/runtime-sessions.json'))
for s in d['sessions']: print(s['id'],s['state'],s['relation'])"
k q; sleep 1.5; shot "after q: the surface exited"
printf '\n##### RESULT: %d step(s) failed\n' "$fail" >> "$LOG"; echo "failed steps: $fail"

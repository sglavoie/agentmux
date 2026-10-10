# Helpers that run on the MacBook, inside the world (after remote-env.sh).
# Everything here only reads state or manages the world's own processes: no
# input is synthesized and nothing needs an accessibility grant.
apppid() { pgrep -f "$APP/Contents/MacOS/Agent Mux" | head -1; }
clients() { priv list-clients -F '#{client_pid} #{session_name} pane=#{pane_id} term=#{client_termname}' 2>/dev/null | sort; }
registry() { python3 - <<'PY'
import json,os
p=os.path.expanduser('~/.local/share/agent-mux/runtime-sessions.json')
for s in json.load(open(p))['sessions']:
    t=s.get('target') or {}
    print(s['id'], s['state'], s.get('relation',''), s.get('project',''), (t.get('instance') or {}).get('kind',''), t.get('ref',''))
PY
}
rsof() { registry | awk -v p="$1" '$2=="live" && $4==p {print $1}' | tail -1; }
pane() { registry | awk -v p="$1" '$2=="live" && $4==p {print $6}' | tail -1; }
screen() { priv capture-pane -p -t "$(pane "$1")" | sed -e 's/[[:space:]]*$//'; }
pfmt() { priv display-message -p -t "$(pane "$1")" "$2"; }
pstate() { pfmt "$1" 'alternate=#{alternate_on} mouse=#{mouse_any_flag} in_history=#{pane_in_mode} position=#{scroll_position} history=#{history_size} pid=#{pane_pid} dead=#{pane_dead}'; }
attached() { clients | grep -c "pane=$(pane "$1") "; }
# textcell PROJECT TEXT: column and row of the first screen line holding TEXT.
textcell() { screen "$1" | python3 -c "
import sys,unicodedata
needle=sys.argv[1]
for row,line in enumerate(sys.stdin.read().split('\n')):
    i=line.find(needle)
    if i>=0:
        print(sum(2 if unicodedata.east_asian_width(c) in 'WF' else 1 for c in line[:i]),row); break
" "$2"; }
vscreen() { obs capture-pane -p -t "=$1:" 2>/dev/null | sed -e 's/[[:space:]]*$//' | cat -s; }
# The app's unified log since it started, without timestamps' date and process columns.
applog() { /usr/bin/log show --start "$(cat $R/app.started)" --style compact --predicate "subsystem == \"io.github.sglavoie.agentmux\" AND processIdentifier == $(apppid)" 2>/dev/null | sed -E -e '1d' -e 's/^[0-9-]+ ([0-9:.]+) +[A-Za-z]+ +Agent Mux\[[0-9:a-f]+\] /\1 /'; }
mark() { applog | wc -l | tr -d ' ' > $R/logmark; }
since() { applog | tail -n +$(( $(cat $R/logmark) + 1 )); }
snapshot() { python3 - <<'PY'
import json,os,socket
s=socket.socket(socket.AF_UNIX); s.settimeout(5); s.connect(os.path.expanduser('~/.local/share/agent-mux/daemon.sock'))
s.sendall((json.dumps({"type":"subscribe","subscribe":{"protocol":1,"client":"acceptance-probe","features":["runtime_sessions"]}})+"\n").encode())
f=s.makefile(); ack=json.loads(f.readline()); frame=json.loads(f.readline())
d=frame['data']
print('snapshot v%s kitty.ready=%s attention.total=%s' % (d.get('v'), d['kitty'].get('ready'), d['attention'].get('total')))
for x in d.get('sessions',[]):
    rt=x.get('runtime') or {}
    print('  %-8s %-8s icon=%-9s attention=%-5s runtime=%s %s %s' % (x.get('project'), x.get('harness'), x.get('icon_state') or '-', x.get('attention',False), rt.get('backend','-'), rt.get('pane',''), rt.get('session_id','')))
PY
}
# The app copy: its own preferences domain, the world's environment, and a
# window frame the driver knows (points; top-left 10,40; 1100 by 700). The
# global shortcuts keep their defaults: no other Agent Mux runs on the MacBook.
app_defaults() {
  defaults delete $BUNDLE >/dev/null 2>&1
  defaults write $BUNDLE amxBinaryPath -string $R/shim/amx
  defaults write $BUNDLE amxUseAppNotifications -bool false
  defaults write $BUNDLE amxEnvironment -dict PATH "$PATH" XDG_STATE_HOME "$XDG_STATE_HOME" XDG_CONFIG_HOME "$XDG_CONFIG_HOME" XDG_DATA_HOME "$XDG_DATA_HOME" TERMINFO_DIRS "$TERMINFO_DIRS"
  defaults write $BUNDLE amxOpenWorkspaceAtLaunch -bool true
  defaults write $BUNDLE "NSWindow Frame AgentMuxWorkspace" -string "$1"
}
app_start() {
  date '+%Y-%m-%d %H:%M:%S' > $R/app.started
  open -n "$APP" --env HOME="$HOME" --env TMPDIR="$TMPDIR" --env PATH="$PATH"
  n=80; while [ $n -gt 0 ]; do [ -n "$(apppid)" ] && applog | grep -q "feed: live" && return 0; sleep 0.25; n=$((n-1)); done; return 1
}
app_quit() { p=$(apppid); [ -n "$p" ] || return 0; kill "$p" 2>/dev/null; n=40; while [ $n -gt 0 ] && kill -0 "$p" 2>/dev/null; do sleep 0.25; n=$((n-1)); done; }
# The link target: python's http.server in the world's log directory.
linkpid() { lsof -nP -t -iTCP:$LINKPORT -sTCP:LISTEN 2>/dev/null | head -1; }
link_start() {
  [ -z "$(linkpid)" ] || { echo "port $LINKPORT is already in use on the MacBook"; return 1; }
  : > $R/logs/link.log
  # Detached with every stream redirected: a shell left waiting on it would
  # keep the SSH session that started it open.
  nohup /bin/sh -c "cd $R/logs && exec python3 -u -m http.server $LINKPORT --bind 127.0.0.1" >> $R/logs/link.log 2>&1 </dev/null &
  n=40; while [ $n -gt 0 ] && [ -z "$(linkpid)" ]; do sleep 0.25; n=$((n-1)); done
  n=20; while [ $n -gt 0 ]; do curl -s -o /dev/null "http://127.0.0.1:$LINKPORT/s15-selfcheck"; sleep 0.5; grep -q "GET /s15-selfcheck" $R/logs/link.log && return 0; n=$((n-1)); done
  return 1
}
link_stop() { p=$(linkpid); [ -n "$p" ] || return 0; ps -o command= -p "$p" | grep -q "http.server $LINKPORT" || { echo "port $LINKPORT is somebody else's"; return 1; }; kill "$p"; }
linkhits() { grep -c "GET /s15-link " $R/logs/link.log 2>/dev/null; }

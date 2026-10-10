# Helpers that run on the MacBook, inside the world (after remote-env.sh).
# Everything here only reads state or manages the world's own processes: no
# input is synthesized and nothing needs an accessibility grant.
apppid() { pgrep -f "$APP/Contents/MacOS/Agent Mux" | head -1; }
daemonpid() { snapshot 2>/dev/null | sed -n 's/.*daemon.pid=\([0-9]*\).*/\1/p'; }
sock() { echo "$HOME/.local/share/agent-mux/daemon.sock"; }
# clients: one line per terminal attached to the private server.
clients() { priv list-clients -F 'client_pid=#{client_pid} tty=#{client_name} pane=#{pane_id} size=#{client_width}x#{client_height}' 2>/dev/null | sort; }
registry() { python3 - <<'PY'
import json,os
p=os.path.expanduser('~/.local/share/agent-mux/runtime-sessions.json')
for s in json.load(open(p))['sessions']:
    t=s.get('target') or {}
    a=s.get('attachment') or {}
    print(s['id'], s['state'], s.get('project',''), t.get('ref','-'), 'pid=%s' % (s.get('process') or {}).get('pid','-'),
          'owner=%s' % (a.get('client','none').replace(' ','_')), 'reason=%s' % (s.get('stale_reason','') or '-').replace(' ','_'))
PY
}
regcount() { registry | wc -l | tr -d ' '; }
# rsof PROJECT / pane PROJECT: the project's most recent registration, live or not.
rsof() { registry | awk -v p="$1" '$3==p {print $1}' | tail -1; }
pane() { registry | awk -v p="$1" '$3==p {print $4}' | tail -1; }
regline() { registry | awk -v p="$1" '$3==p' | tail -1; }
pfmt() { priv display-message -p -t "$(pane "$1")" "$2"; }
size() { pfmt "$1" '#{window_width}x#{window_height}'; }
attached() { clients | grep -c "pane=$(pane "$1") "; }
panes() { priv list-panes -a -F '#{pane_id} pid=#{pane_pid} dead=#{pane_dead} status=#{pane_dead_status} size=#{pane_width}x#{pane_height}' 2>&1; }
screen() { priv capture-pane -p -t "$(pane "$1")" | sed -e 's/[[:space:]]*$//' | cat -s; }
# agentproc PROJECT: the pane's process as the operating system reports it:
# PID and start time. The same line twice is the same process.
agentpid() { registry | awk -v p="$1" '$3==p {print $5}' | tail -1 | cut -d= -f2; }
agentproc() { LC_ALL=C TZ=UTC ps -o pid=,lstart= -p "$(agentpid "$1")" 2>/dev/null | sed -e 's/^ *//' -e 's/  *$//' -e 's/  */ /g'; }
lasttick() { tail -1 $R/logs/ticks-$1.log; }
# tickgaps PROJECT: counts that started over, or skipped, in the agent's own log.
tickgaps() { awk 'NR>1 && ($1!=prev+1 || $2!=pid) {bad++} {prev=$1; pid=$2} END {print bad+0}' $R/logs/ticks-$1.log; }
vscreen() { obs capture-pane -p -t "=$1:" 2>/dev/null | sed -e 's/[[:space:]]*$//' | cat -s; }
# The app's unified log for its current process.
applog() { /usr/bin/log show --start "$(cat $R/app.started)" --style compact --predicate "subsystem == \"io.github.sglavoie.agentmux\" AND processIdentifier == $(apppid)" 2>/dev/null | sed -E -e '1d' -e 's/^[0-9-]+ ([0-9:.]+) +[A-Za-z]+ +Agent Mux\[[0-9:a-f]+\] /\1 /'; }
mark() { applog | wc -l | tr -d ' ' > $R/logmark; }
since() { applog | tail -n +$(( $(cat $R/logmark) + 1 )); }
prefs() { echo "window_open=$(defaults read $BUNDLE amxWorkspaceWindowOpen 2>/dev/null || echo unset) place=$(defaults read $BUNDLE amxWorkspacePlace 2>/dev/null || echo unset)"; }
snapshot() { python3 - <<'PY'
import json,os,socket
try:
    s=socket.socket(socket.AF_UNIX); s.settimeout(5); s.connect(os.path.expanduser('~/.local/share/agent-mux/daemon.sock'))
except OSError as e:
    print('no daemon: %s' % e); raise SystemExit(1)
s.sendall((json.dumps({"type":"subscribe","subscribe":{"protocol":1,"client":"acceptance-probe","features":["runtime_sessions"]}})+"\n").encode())
f=s.makefile(); ack=json.loads(f.readline()); frame=json.loads(f.readline())
d=frame['data']
print('snapshot v%s daemon.pid=%s attention.total=%s' % (d.get('v'), d['daemon'].get('pid'), d['attention'].get('total')))
for x in d.get('sessions',[]):
    rt=x.get('runtime') or {}
    print('  %-6s icon=%-9s attention=%-5s runtime=%s %s %s' % (x.get('project'), x.get('icon_state') or '-', x.get('attention',False), rt.get('backend','-'), rt.get('pane',''), rt.get('session_id','')))
PY
}
listed() { snapshot | grep -c " $1\$"; }
dview() { python3 $R/dview.py "$@"; }
check_json() { amx attach "$1" --json 2>&1 | python3 -c "
import json,sys
d=json.load(sys.stdin)
e=d.get('error')
print('error %s: %s' % (e['code'], e['message']) if e else 'live clients=%s pid=%s' % (d['data'].get('clients'), d['data'].get('pid')))"; }
# The app copy: its own preferences domain and the world's environment. It
# opens its workspace at launch the first time; after that the app itself
# remembers whether the window was open and where it was.
app_defaults() {
  defaults delete $BUNDLE >/dev/null 2>&1
  defaults write $BUNDLE amxBinaryPath -string $R/shim/amx
  defaults write $BUNDLE amxUseAppNotifications -bool false
  defaults write $BUNDLE amxEnvironment -dict PATH "$PATH" XDG_STATE_HOME "$XDG_STATE_HOME" XDG_CONFIG_HOME "$XDG_CONFIG_HOME" XDG_DATA_HOME "$XDG_DATA_HOME" TERMINFO_DIRS "$TERMINFO_DIRS"
  defaults write $BUNDLE amxOpenWorkspaceAtLaunch -bool true
}
app_start() {
  [ -f $R/app.started ] || date '+%Y-%m-%d %H:%M:%S' > $R/app.started
  open -n "$APP" --env HOME="$HOME" --env TMPDIR="$TMPDIR" --env PATH="$PATH"
  n=120; while [ $n -gt 0 ]; do [ -n "$(apppid)" ] && applog | grep -q "feed: live" && return 0; sleep 0.25; n=$((n-1)); done; return 1
}
app_quit() { p=$(apppid); [ -n "$p" ] || return 0; kill "$p" 2>/dev/null; n=40; while [ $n -gt 0 ] && kill -0 "$p" 2>/dev/null; do sleep 0.25; n=$((n-1)); done; }
daemon_start() {
  nohup amx daemon start >> $R/logs/daemon.out 2>&1 </dev/null &
  n=60; while [ $n -gt 0 ] && ! snapshot >/dev/null 2>&1; do sleep 0.25; n=$((n-1)); done
  snapshot >/dev/null 2>&1
}
# A second terminal, somewhere else: `amx attach` on a pseudo-terminal of its
# own size, held open until ext_close. Its screen is not shown anywhere.
ext_open() {
  nohup /bin/sh -c "sleep 100000 | script -q $R/logs/ext-terminal.out /bin/sh -c 'stty cols $2 rows $3; exec $R/bin/amx attach $1'" > $R/logs/ext-terminal.err 2>&1 </dev/null &
  echo $! > $R/ext.pid
}
ext_close() { [ -f $R/ext.pid ] || return 0; p=$(cat $R/ext.pid); pkill -P "$p" 2>/dev/null; kill "$p" 2>/dev/null; rm -f $R/ext.pid; }
# daemonlog: what the world's daemons logged about sessions they found changed.
daemonlog() { python3 - <<'PY'
import json,os
for line in open(os.path.expanduser('~/.local/state/agent-mux/daemon/daemon.jsonl')):
    try: d=json.loads(line)
    except ValueError: continue
    m=d.get('msg','')
    if 'no longer running' in m or 'released the ownership' in m or 'competing' in m:
        extra=' '.join('%s=%s' % (k,d[k]) for k in ('runtime_session','outcome','reason','client','session') if k in d)
        print(d.get('time','')[11:19], m, extra)
PY
}

# Helpers that run on the MacBook, inside the world (after remote-env.sh).
# Everything here reads state, runs the scripts under test, or manages the
# world's own processes: no input is synthesized.

# --- The scripts under test ---------------------------------------------------
install_app() { AMX_APP_SOURCE="$1" AMX_APP_INSTALL_DIR="${2:-$INSTALL}" AMX_APP_NO_LAUNCH=1 "$COPY/macos/scripts/install-app.sh" </dev/null; }
uninstall_app() { AMX_APP_INSTALL_DIR="$INSTALL" "$COPY/macos/scripts/uninstall-app.sh" "$@" </dev/null; }
# make_v2: the update. The same sources with another version, and a tmux
# runtime whose checksums differ (its VERSION carries one more line), signed
# by the repository's own sign.sh, as a later build would be.
make_v2() {
  rm -rf "$R/v2"; mkdir -p "$R/v2"; ditto "$SRC" "$V2"
  /usr/libexec/PlistBuddy -c "Set :CFBundleShortVersionString 0.1.1" -c "Set :CFBundleVersion 2" "$V2/Contents/Info.plist"
  printf 'build=acceptance-update\n' >> "$V2/Contents/Resources/tmux/VERSION"
  AMX_CODESIGN_IDENTITY=- "$COPY/macos/scripts/sign.sh" "$V2"
}
bundle_version() { /usr/libexec/PlistBuddy -c "Print :CFBundleShortVersionString" "$1/Contents/Info.plist" 2>/dev/null; }

# --- The app ----------------------------------------------------------------------
apppid() { pgrep -f "$DEST/Contents/MacOS/Agent Mux" | head -1; }
app_defaults() {
  defaults write $BUNDLE amxUseAppNotifications -bool false
}
app_start() {
  date '+%Y-%m-%d %H:%M:%S' > $R/app.started
  open -n "$DEST" --env HOME="$HOME" --env TMPDIR="$TMPDIR" --env PATH="$PATH"
  n=40; while [ $n -gt 0 ] && [ -z "$(apppid)" ]; do sleep 0.25; n=$((n-1)); done; [ -n "$(apppid)" ]
}
app_quit() { osascript -e "tell application id \"$BUNDLE\" to quit" >/dev/null 2>&1; n=40; while [ $n -gt 0 ] && [ -n "$(apppid)" ]; do sleep 0.25; n=$((n-1)); done; [ -z "$(apppid)" ]; }
# The app's unified log for its current process.
applog() { /usr/bin/log show --start "$(cat $R/app.started)" --style compact --predicate "subsystem == \"io.github.sglavoie.agentmux\" AND processIdentifier == $(apppid)" 2>/dev/null | sed -E -e '1d' -e 's/^[0-9-]+ ([0-9:.]+) +[A-Za-z]+ +Agent Mux\[[0-9:a-f]+\] /\1 /'; }

# --- amx, the daemon, the registry ------------------------------------------------
status() { "$AMX" backend status --json 2>/dev/null; }
field() { plutil -extract "data.$1" raw -o - - 2>/dev/null; }
sfield() { status | field "$1"; }
daemon_start() {
  nohup "$AMX" daemon start >> $R/logs/daemon.out 2>&1 </dev/null &
  n=60; while [ $n -gt 0 ] && ! snapshot >/dev/null 2>&1; do sleep 0.25; n=$((n-1)); done
  snapshot >/dev/null 2>&1
}
snapshot() { python3 - <<'PY'
import json,os,socket
try:
    s=socket.socket(socket.AF_UNIX); s.settimeout(5); s.connect(os.path.expanduser('~/.local/share/agent-mux/daemon.sock'))
except OSError as e:
    print('no daemon: %s' % e); raise SystemExit(1)
s.sendall((json.dumps({"type":"subscribe","subscribe":{"protocol":1,"client":"acceptance-probe","features":["runtime_sessions"]}})+"\n").encode())
f=s.makefile(); ack=json.loads(f.readline()); frame=json.loads(f.readline())
d=frame['data']
print('snapshot v%s daemon.pid=%s' % (d.get('v'), d['daemon'].get('pid')))
for x in d.get('sessions',[]):
    rt=x.get('runtime') or {}
    print('  %-6s runtime=%s %s %s' % (x.get('project'), rt.get('backend','-'), rt.get('pane',''), rt.get('session_id','')))
PY
}
daemonpid() { snapshot 2>/dev/null | sed -n 's/.*daemon.pid=\([0-9]*\).*/\1/p'; }
registry() { python3 - <<'PY'
import json,os
p=os.path.expanduser('~/.local/share/agent-mux/runtime-sessions.json')
for s in json.load(open(p))['sessions']:
    t=s.get('target') or {}
    print(s['id'], s['state'], s.get('project',''), t.get('ref','-'), 'pid=%s' % (s.get('process') or {}).get('pid','-'),
          'generation=%s' % (t.get('instance') or {}).get('generation','-'), 'reason=%s' % (s.get('stale_reason','') or '-').replace(' ','_'))
PY
}
rsof() { registry | awk -v p="$1" '$3==p {print $1}' | tail -1; }
regline() { registry | awk -v p="$1" '$3==p' | tail -1; }
launch() { "$AMX" project launch "$1" --profile work --backend tmux --json > $R/logs/launch-$1.json 2>&1; }

# --- The agents ---------------------------------------------------------------------
# agentproc PROJECT: the pane's process as the operating system reports it:
# PID and start time. The same line twice is the same process.
agentpid() { registry | awk -v p="$1" '$3==p {print $5}' | tail -1 | cut -d= -f2; }
agentproc() { LC_ALL=C TZ=UTC ps -o pid=,lstart= -p "$(agentpid "$1")" 2>/dev/null | sed -e 's/^ *//' -e 's/  *$//' -e 's/  */ /g'; }
lasttick() { tail -1 $R/logs/ticks-$1.log; }
# tickgaps PROJECT: counts that started over, or skipped, in the agent's own log.
tickgaps() { awk 'NR>1 && ($1!=prev+1 || $2!=pid) {bad++} {prev=$1; pid=$2} END {print bad+0}' $R/logs/ticks-$1.log; }
# ticklate PROJECT: seconds between consecutive counts that were more than 3 apart.
ticklate() { awk 'NR>1 && $3-prev>3 {bad++} {prev=$3} END {print bad+0}' $R/logs/ticks-$1.log; }

# --- The session server, asked directly ------------------------------------------------
pin() { cat "$SRV/tmux.binary" 2>/dev/null; }
srv() { "$(pin)" -S "$SRV/tmux.sock" "$@"; }
serverpid() { srv display-message -p '#{pid}' 2>/dev/null; }
# serverproc: the server process as the operating system reports it.
serverproc() { p=$(serverpid); [ -n "$p" ] && LC_ALL=C TZ=UTC ps -o pid=,lstart= -p "$p" | sed -e 's/^ *//' -e 's/  *$//' -e 's/  */ /g'; }
# serverexe: the executable file the server process is running.
serverexe() { p=$(serverpid); [ -n "$p" ] && ps -o comm= -p "$p"; }
runtimes() { ls -d $DATA/runtime/tmux-* 2>/dev/null | grep -v retired; }

# --- What must be left alone --------------------------------------------------------
# userdata: a digest of every file that is the operator's, not the app's.
userdata() { (cd $HOME && find .config/agent-mux $DATA/handoffs $DATA/worktrees $DATA/launch_overrides.json $DATA/transcripts.json -type f 2>/dev/null | LC_ALL=C sort | xargs shasum -a 256 | shasum -a 256 | cut -c1-16); }
# realstate: the MacBook user's own AgentMux files, which nothing here may touch.
realstate() { ls -laT "$REAL_HOME/.local/bin/amx" "$REAL_HOME/.config/agent-mux" "$REAL_HOME/.local/share/agent-mux" 2>&1 | shasum -a 256 | cut -c1-16; }
worldprocs() { pgrep -fl "$R/" | grep -v "pgrep" ; }

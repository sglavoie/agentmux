# Sourced on the MacBook by everything that runs there: the world's own HOME
# and TMPDIR, and a PATH of the world's bin and the system directories only.
# No Homebrew: the only tmux is the one the packaged app brings, and the only
# amx is the one inside the packaged app. Nothing here reads or writes the
# MacBook user's own AgentMux state.
R=/tmp/amxs19
REAL_HOME=${REAL_HOME:-$HOME}
export REAL_HOME
export HOME=$R/home TMPDIR=$R/tmp PATH=$R/bin:/usr/bin:/bin:/usr/sbin:/sbin
unset KITTY_LISTEN_ON KITTY_WINDOW_ID KITTY_PID KITTY_PUBLIC_KEY KITTY_INSTALLATION_DIR
unset AMX_CONFIG AMX_TMUX AMX_TMUX_RUNTIME AMX_RUNTIME_SESSION_ID CLAUDECODE TMUX TMUX_PANE ZDOTDIR
unset XDG_STATE_HOME XDG_CONFIG_HOME XDG_DATA_HOME XDG_CACHE_HOME
export TERM=xterm-256color LC_ALL=en_US.UTF-8
BUNDLE=io.github.sglavoie.agentmux
INSTALL=$R/Applications
DEST="$INSTALL/Agent Mux.app"
AMX="$DEST/Contents/Resources/bin/amx"      # the packaged amx: the only one in the world
TM="$DEST/Contents/Resources/tmux/bin/tmux" # the packaged tmux, used by the driver itself to look
COPY=$(cat $R/copy 2>/dev/null)             # the checkout copy the bundle was built in
SRC="$COPY/macos/build/Agent Mux.app"
DATA=$HOME/.local/share/agent-mux
KITTY=/Applications/kitty.app/Contents/MacOS
KSOCK=unix:$R/kitty.sock
priv() { "$TM" -S $TMPDIR/agent-mux-$(id -u)/tmux.sock "$@"; }
obs() { "$TM" -S $R/obs.sock -f /dev/null "$@"; }
K() { $KITTY/kitten @ --to $KSOCK "$@"; }
registry() { python3 - <<'PY'
import json,os
p=os.path.expanduser('~/.local/share/agent-mux/runtime-sessions.json')
for s in json.load(open(p))['sessions']:
    t=s.get('target') or {}
    print(s['id'], s['state'], s.get('relation',''), (t.get('instance') or {}).get('kind','-'), t.get('ref','-'),
          'project=%s' % s.get('project',''), 'pid=%s' % (s.get('process') or {}).get('pid','-'),
          'conv=%s' % ((s.get('conversation') or {}).get('session_id')), 'parent=%s' % s.get('parent'))
PY
}
# realstate: the MacBook user's own amx and AgentMux directories, which no
# part of the run may change. Taken at setup and compared at teardown.
realstate() {
  for p in "$REAL_HOME/.local/bin/amx" "$REAL_HOME/.config/agent-mux" "$REAL_HOME/.local/share/agent-mux" "$REAL_HOME/.local/state/agent-mux" "$REAL_HOME/.config/kitty" "/Applications/Agent Mux.app"; do
    if [ -e "$p" ]; then /usr/bin/stat -f '%N %z %m' "$p"; [ -d "$p" ] && (cd "$p" && find . -maxdepth 2 -not -name '*.log' -not -name '*.jsonl' -not -name '*.sock' | sort | head -200 | shasum -a 256 | cut -c1-16); else echo "$p absent"; fi
  done
  echo "preferences: $(HOME=$REAL_HOME defaults read $BUNDLE 2>/dev/null | shasum -a 256 | cut -c1-16)"
  echo "kitty processes: $(pgrep -x kitty | wc -l | tr -d ' ')"
}

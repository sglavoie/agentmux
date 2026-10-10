# Sourced on the MacBook by everything that runs there: the world's own HOME
# and TMPDIR, an install root of its own, and a PATH with the system
# directories only: no Homebrew, so no tmux but the one the app brings.
# Nothing here reads or writes the MacBook user's own AgentMux state.
R=/tmp/amxs17
REAL_HOME=${REAL_HOME:-$HOME}
export REAL_HOME
export HOME=$R/home TMPDIR=$R/tmp PATH=$R/bin:/usr/bin:/bin:/usr/sbin:/sbin
unset KITTY_LISTEN_ON KITTY_WINDOW_ID KITTY_PID AMX_CONFIG AMX_TMUX AMX_TMUX_RUNTIME AMX_RUNTIME_SESSION_ID CLAUDECODE TMUX TMUX_PANE
unset XDG_STATE_HOME XDG_CONFIG_HOME XDG_DATA_HOME XDG_CACHE_HOME
export TERM=xterm-256color LC_ALL=en_US.UTF-8
BUNDLE=io.github.sglavoie.agentmux
INSTALL=$R/Applications
DEST="$INSTALL/Agent Mux.app"
AMX="$DEST/Contents/Resources/bin/amx"
COPY=$(cat $R/copy 2>/dev/null)            # the checkout copy the bundle was built in
SRC="$COPY/macos/build/Agent Mux.app"       # what `just mac-app Release` built
V2="$R/v2/Agent Mux.app"                    # the update (see make_v2)
DATA=$HOME/.local/share/agent-mux
SRV=$TMPDIR/agent-mux-$(id -u)

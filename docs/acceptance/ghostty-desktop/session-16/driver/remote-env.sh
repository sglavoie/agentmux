# Sourced on the MacBook by everything that runs there: the isolated world's
# own HOME, TMPDIR, config, daemon, registry, and private tmux (a bundled
# copy, nothing installed). Nothing here reads or writes the MacBook user's
# own AgentMux state.
R=/tmp/amxs16
REAL_HOME=${REAL_HOME:-$HOME}
export REAL_HOME
export HOME=$R/home TMPDIR=$R/tmp PATH=$R/bin:$R/tmux/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin
export XDG_STATE_HOME=$R/home/.local/state XDG_CONFIG_HOME=$R/home/.config XDG_DATA_HOME=$R/home/.local/share
export TERMINFO_DIRS=$R/tmux/share/terminfo:/usr/share/terminfo
unset KITTY_LISTEN_ON KITTY_WINDOW_ID KITTY_PID AMX_CONFIG AMX_RUNTIME_SESSION_ID CLAUDECODE TMUX TMUX_PANE
export TERM=xterm-256color LC_ALL=en_US.UTF-8
BUNDLE=io.github.sglavoie.agentmux.acceptance16
APP="$R/app/Agent Mux.app"
priv() { tmux -S $R/tmp/agent-mux-$(id -u)/tmux.sock "$@"; }
obs() { $R/obs.sh "$@"; }

# The isolated world every session-14 acceptance command runs in: its own HOME,
# TMPDIR, config, daemon, runtime-session registry, private tmux server, and
# ASL state. Nothing here reads or writes the operator's.
R=/tmp/amxs14
export HOME=$R/home TMPDIR=$R/tmp PATH=$R/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin
export XDG_STATE_HOME=$R/home/.local/state XDG_CONFIG_HOME=$R/home/.config XDG_DATA_HOME=$R/home/.local/share
export AGENT_PLANS_DIR=$R/plans
unset KITTY_LISTEN_ON KITTY_WINDOW_ID KITTY_PID KITTY_PUBLIC_KEY KITTY_INSTALLATION_DIR AMX_CONFIG AMX_RUNTIME_SESSION_ID CLAUDECODE TMUX TMUX_PANE
for v in $(env | grep -E '^(ASL_|AMX_|CLAUDE_CODE|CLAUDE_)' | cut -d= -f1); do unset $v; done
export TERM=xterm-256color LC_ALL=en_US.UTF-8
# The bundle identifier of the app copy under test: its own preferences
# domain, and not the installed app's single instance.
BUNDLE=io.github.sglavoie.agentmux.acceptance14
APP="$R/app/Agent Mux.app"

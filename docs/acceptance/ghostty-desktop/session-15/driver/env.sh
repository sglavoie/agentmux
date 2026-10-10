# The isolated world every session-15 acceptance command runs in: its own HOME,
# TMPDIR, config, daemon, runtime-session registry, and private tmux server.
# Nothing here reads or writes the operator's AgentMux state. The three real
# harnesses are the exception by design: agent.sh starts each with the
# operator's own home and credentials, and with no AgentMux identity.
R=/tmp/amxs15
# The operator's own home, kept for the two things that must use it: the real
# harnesses (agent.sh) and the app copy's preferences domain.
REAL_HOME=${REAL_HOME:-$HOME}
export REAL_HOME
export HOME=$R/home TMPDIR=$R/tmp PATH=$R/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin
export XDG_STATE_HOME=$R/home/.local/state XDG_CONFIG_HOME=$R/home/.config XDG_DATA_HOME=$R/home/.local/share
unset KITTY_LISTEN_ON KITTY_WINDOW_ID KITTY_PID KITTY_PUBLIC_KEY KITTY_INSTALLATION_DIR KITTY_SHELL_INTEGRATION AMX_CONFIG AMX_RUNTIME_SESSION_ID CLAUDECODE TMUX TMUX_PANE
for v in $(env | grep -E '^(ASL_|AMX_|CLAUDE_CODE|CLAUDE_|CODEX_|COPILOT_)' | cut -d= -f1); do unset $v; done
export TERM=xterm-256color LC_ALL=en_US.UTF-8
# The bundle identifier of the app copy under test: its own preferences
# domain, and not the installed app's single instance.
BUNDLE=io.github.sglavoie.agentmux.acceptance15
APP="$R/app/Agent Mux.app"
# The port the link target listens on.
LINKPORT=47615

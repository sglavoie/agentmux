# Sourced on the Studio by the driver. The Studio only orchestrates: it sends
# scripts over SSH and reads what comes back. Everything under test (the
# installer, the app, amx, the daemon, the tmux server, the agents) runs on
# the MacBook (AMX_MAC_TEST_HOST). No input is synthesized on either machine.
: "${AMX_MAC_TEST_HOST:?the Mac that is installed on}"
H=$AMX_MAC_TEST_HOST
R=/tmp/amxs17            # the world, on the MacBook
SSH="ssh -o StrictHostKeyChecking=yes -o ConnectTimeout=20 -o ServerAliveInterval=30"
# rr: runs a sh script (standard input) on the MacBook inside the world.
rr() { { echo ". $R/remote-env.sh"; echo ". $R/remote-lib.sh"; cat; } | $SSH "$H" /bin/sh -s; }
# rq 'COMMAND': the same for a one-line command.
rq() { printf '%s\n' "$1" | rr; }

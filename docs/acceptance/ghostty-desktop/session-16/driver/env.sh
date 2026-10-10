# Sourced on the Studio by the driver. The Studio only orchestrates: it sends
# scripts over SSH and reads what comes back. Every process under test, every
# window, and the sleep and wake are the MacBook's (AMX_MAC_TEST_HOST). No
# input is synthesized on either machine.
: "${AMX_MAC_TEST_HOST:?the Mac that shows the UI}"
H=$AMX_MAC_TEST_HOST
R=/tmp/amxs16            # the world, on the MacBook
L=/tmp/amxs16r           # scratch on the Studio: the built amx, tmux copy, logs
SSH="ssh -o StrictHostKeyChecking=yes -o ConnectTimeout=20 -o ServerAliveInterval=30"
# rr: runs a sh script (standard input) on the MacBook inside the world.
rr() { { echo ". $R/remote-env.sh"; echo ". $R/remote-lib.sh"; cat; } | $SSH "$H" /bin/sh -s; }
# rq 'COMMAND': the same for a one-line command.
rq() { printf '%s\n' "$1" | rr; }

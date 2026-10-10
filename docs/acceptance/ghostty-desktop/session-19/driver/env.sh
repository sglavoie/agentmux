# Sourced on the Studio by run.sh. The Studio only orchestrates: it copies the
# driver to the MacBook, starts its scripts over SSH, and brings the logs
# back. Everything seen or driven (Kitty, the app, amx, tmux) is on the
# MacBook (AMX_MAC_TEST_HOST).
: "${AMX_MAC_TEST_HOST:?the Mac that shows and drives the UI}"
H=$AMX_MAC_TEST_HOST
R=/tmp/amxs19            # the world, on the MacBook
SSH="ssh -o StrictHostKeyChecking=yes -o ConnectTimeout=20 -o ServerAliveInterval=30"
# rs SCRIPT ARG...: runs one of the driver's scripts on the MacBook.
rs() { s=$1; shift; $SSH "$H" /bin/bash "$R/driver/$s" "$@" </dev/null; }

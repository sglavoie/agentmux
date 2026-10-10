# Sourced on the Studio by the driver. The Studio only orchestrates: it sends
# scripts over SSH, speaks VNC without a viewer, and runs the three harness
# processes headless. Every window, key, pointer move, and pixel is the
# MacBook's (AMX_MAC_TEST_HOST).
: "${AMX_MAC_TEST_HOST:?the Mac that shows the UI}"
H=$AMX_MAC_TEST_HOST
R=/tmp/amxs15            # the world, on the MacBook
L=/tmp/amxs15r           # scratch on the Studio: harness directories, logs, pictures
# The viewer uses the shared VNC password. That reaches the logged-in desktop
# only while the MacBook's VNCAlwaysStartOnConsole preference is true (see
# README); otherwise the viewer is given a login-window session of its own.
export AMX_VNC_PASSWORD_FILE=${AMX_VNC_PASSWORD_FILE:-$HOME/Downloads/pass}
SSH="ssh -o StrictHostKeyChecking=yes -o ConnectTimeout=20 -o ServerAliveInterval=30"
# rr: runs a sh script (standard input) on the MacBook inside the world.
rr() { { echo ". $R/remote-env.sh"; cat; } | $SSH "$H" /bin/sh -s; }
# r1 'COMMAND': the same for a one-line command.
r1() { printf '%s\n' "$1" | rr; }
# One held VNC connection for the whole run (see vnc.py daemon).
export AMX_VNC_SOCKET=$L/vnc.sock
vnc_up() { [ -S $AMX_VNC_SOCKET ] && return 0; ( AMX_VNC_SOCKET= nohup python3 "$(dirname "${BASH_SOURCE[0]}")/vnc.py" daemon $L/vnc.sock > $L/logs/vnc.log 2>&1 </dev/null & ); n=40; while [ $n -gt 0 ] && [ ! -S $L/vnc.sock ]; do sleep 0.25; n=$((n-1)); done; [ -S $L/vnc.sock ]; }
vnc_down() { [ -S $AMX_VNC_SOCKET ] && python3 "$(dirname "${BASH_SOURCE[0]}")/vnc.py" quit; }

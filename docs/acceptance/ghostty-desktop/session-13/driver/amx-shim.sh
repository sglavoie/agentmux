#!/bin/sh
# The amx the app under test runs. libghostty starts a terminal's command
# through login(1), which resets HOME to the account's real one; this puts the
# isolated world's HOME back before the real amx reads its registry. Nothing
# else differs from running /tmp/amxs13/bin/amx directly.
export HOME=/tmp/amxs13/home
exec /tmp/amxs13/bin/amx "$@"

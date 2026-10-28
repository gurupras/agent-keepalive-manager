#!/bin/sh
# The executable of /Applications/agent-keepalive.app: start the server if it
# is not running and open the UI in the default browser.
exec /usr/local/bin/agent-keepalive open "$@"

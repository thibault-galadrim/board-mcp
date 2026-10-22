#!/bin/sh
# Kills the spike server and starts a fresh process (sessions forgotten).
set -eu
cd "$(dirname "$0")/../.."
pkill -f 'bin/mcphttp' || true
sleep 0.5
BOARD_SPIKE_TOKEN="$(cat bin/spike-token)" nohup bin/mcphttp ${BOARD_SPIKE_FLAGS:-} >> bin/mcphttp.log 2>&1 &
sleep 0.5
echo "restarted pid=$(pgrep -f 'bin/mcphttp')"

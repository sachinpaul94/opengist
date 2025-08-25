#!/bin/sh
set -euo pipefail
echo "-----------------------------"
echo "Watching frontend and backend for changes..."
(cd public && npm install) && cd -
make watch_frontend &
make watch_backend &

trap 'kill $(jobs -p)' EXIT
wait

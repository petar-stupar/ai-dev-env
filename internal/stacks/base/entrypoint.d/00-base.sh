#!/usr/bin/env bash
# Runs first: say what this image was built from, so `docker logs` shows it.
set -euo pipefail
# shellcheck source=/dev/null
. /etc/aide/lib.sh

log "stacks: $AIDE_STACKS"

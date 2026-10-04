#!/bin/sh
# The credentials volume starts empty; give Claude Code its config dir on it.
. /etc/aide/lib.sh
: "${CLAUDE_CONFIG_DIR:=$AIDE_CREDS/claude}"
as_agent mkdir -p "$CLAUDE_CONFIG_DIR"

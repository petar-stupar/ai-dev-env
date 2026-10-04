#!/bin/sh
# The credentials volume starts empty; recreate the target of the
# ~/.local/share/opencode symlink, and the global skills dir.
. /etc/aide/lib.sh
as_agent mkdir -p "$AIDE_CREDS/opencode" "$AGENT_HOME/.config/opencode/skills"

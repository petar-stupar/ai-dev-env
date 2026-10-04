#!/bin/bash
# The credentials volume starts empty; gh needs its config dir to exist and be agent-owned.
. /etc/aide/lib.sh
as_agent mkdir -p "$AIDE_CREDS/gh"

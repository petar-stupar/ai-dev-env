#!/usr/bin/env bash
# Prepares the volumes, runs every stack's entrypoint.d hook, then hands over
# to code-server as `agent`.
#
# Runs as root because hooks may need to mount 9P (CAP_SYS_ADMIN). Everything
# that executes user code is dropped to `agent` first.
set -euo pipefail

# lib.sh is shellchecked on its own.
# shellcheck source=/dev/null
. /etc/aide/lib.sh

CODE_SERVER_PORT="${CODE_SERVER_PORT:-8080}"
CODE_SERVER_PASSWORD="${CODE_SERVER_PASSWORD:-agent}"

dirs=("$AGENT_HOME/mnt" "$AIDE_CACHE" /var/log/aide)
if [ -r /etc/aide/cache-dirs ]; then
    while IFS= read -r d; do
        [ -n "$d" ] && dirs+=("$AIDE_CACHE/$d")
    done </etc/aide/cache-dirs
fi
install -d -o "$AGENT_UID" -g "$AGENT_GID" "${dirs[@]}"
install -d -o "$AGENT_UID" -g "$AGENT_GID" -m 0700 "$AIDE_CREDS"
mkdir -p "$WORKSPACE"

# The creds and cache volumes are ours: a fresh volume, or one written under a
# different uid, gets handed to agent. -xdev keeps find off anything mounted
# inside them. Bind mounts belong to the host and are never chowned.
for vol in "$AIDE_CREDS" "$AIDE_CACHE"; do
    find "$vol" -xdev ! -user "$AGENT_USER" -exec chown -h "$AGENT_UID:$AGENT_GID" {} + \
        || warn "could not fix ownership under $vol"
done

# The workspace is a bind mount from the host, so its ownership is whatever the
# host uses. Claim the top directory when we can; a read-only or root-owned
# mount is not fatal, so only warn.
if ! chown "$AGENT_UID:$AGENT_GID" "$WORKSPACE" 2>/dev/null; then
    warn "could not chown $WORKSPACE; the agent user may not be able to write to it"
fi

for hook in /etc/aide/entrypoint.d/*.sh; do
    [ -e "$hook" ] || continue
    bash "$hook" || warn "hook $(basename "$hook") failed (exit $?)"
done

log "starting code-server on 0.0.0.0:$CODE_SERVER_PORT"
exec setpriv --reuid="$AGENT_UID" --regid="$AGENT_GID" --init-groups \
    env HOME="$AGENT_HOME" USER="$AGENT_USER" LOGNAME="$AGENT_USER" \
        XDG_CACHE_HOME="${XDG_CACHE_HOME:-$AIDE_CACHE/xdg}" \
        PASSWORD="$CODE_SERVER_PASSWORD" \
    code-server \
        --bind-addr "0.0.0.0:$CODE_SERVER_PORT" \
        --auth password \
        --disable-telemetry \
        --disable-update-check \
        "$WORKSPACE"

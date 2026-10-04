#!/bin/bash
# Serve and mount dotnetdocfs at ~/mnt/dotnetdocfs, then hand its skill to
# the agents. One server per container, running as agent; warn and carry on
# on any failure so the container still starts.
set -u
# shellcheck source=/dev/null
. /etc/aide/lib.sh

m="$AGENT_HOME/mnt/dotnetdocfs"
port="${DOTNETDOCFS_PORT:-15640}"
logfile=/var/log/aide/dotnetdocfs.log

if ! command -v dotnetdoc >/dev/null 2>&1; then
    warn "dotnetdoc not on PATH; skipping"
    exit 0
fi
if ! has_stack dotnet8 && ! has_stack dotnet10; then
    warn "no .NET SDK stack selected (dotnet8/dotnet10); dotnetdocfs will have no docs to serve"
fi

install -d -o "$AGENT_UID" -g "$AGENT_GID" "$m" || warn "could not create $m"

if mountpoint -q "$m" && pgrep -u "$AGENT_USER" -x dotnetdoc >/dev/null 2>&1; then
    log "dotnetdocfs already serving at $m"
else
    # A mount left behind without its server (snapshot, crash) would hang
    # every access; detach it before mounting afresh.
    if mountpoint -q "$m"; then
        umount -l "$m" || warn "could not unmount stale $m"
    fi
    log "starting dotnetdocfs on 127.0.0.1:$port, mounting at $m (log: $logfile)"
    # setsid -f detaches it from this hook so it outlives the entrypoint's
    # hook loop; dotnetdoc mounts itself via sudo since it is not root.
    as_agent setsid -f dotnetdoc --mount --path "$m" --port "$port" >>"$logfile" 2>&1 \
        || warn "could not start dotnetdoc"
    if ! wait_for_mount "$m" 15; then
        warn "dotnetdocfs is not mounted; see $logfile (needs --cap-add SYS_ADMIN, and apparmor=unconfined on AppArmor hosts)"
        exit 0
    fi
    log "dotnetdocfs mounted at $m"
fi

install_skill "$m/skills/dotnet-api-docs/SKILL.md" "$m" dotnet-api-docs \
    || warn "could not install the dotnet-api-docs skill"

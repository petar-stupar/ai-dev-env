#!/usr/bin/env bash
# Starts the 9P servers, mounts them, then hands over to code-server as `agent`.
#
# Runs as root because mounting 9P needs CAP_SYS_ADMIN. Everything that executes
# user code -- the 9P servers and code-server -- is dropped to `agent` first, so
# nothing the agent runs has root.
set -euo pipefail

AGENT_USER=agent
AGENT_HOME=/home/agent
WORKSPACE="${WORKSPACE:-$AGENT_HOME/workspace}"
TERMINALFS_PORT="${TERMINALFS_PORT:-15641}"
DOTNETDOCFS_PORT="${DOTNETDOCFS_PORT:-15640}"
TERMINALFS_MNT="$AGENT_HOME/mnt/terminalfs"
DOTNETDOCFS_MNT="$AGENT_HOME/mnt/dotnetdocfs"
CODE_SERVER_PORT="${CODE_SERVER_PORT:-8080}"
CODE_SERVER_PASSWORD="${CODE_SERVER_PASSWORD:-agent}"

AGENT_UID="$(id -u "$AGENT_USER")"
AGENT_GID="$(id -g "$AGENT_USER")"

log() { printf '%s %s\n' "[entrypoint]" "$*"; }
warn() { printf '%s %s\n' "[entrypoint] WARNING:" "$*" >&2; }

# setpriv changes the credentials but not the environment, and root's HOME would
# send terminalfs looking for its deny list, and dotnetdoc for the NuGet package
# folder, in the wrong place.
as_agent() {
    setpriv --reuid="$AGENT_UID" --regid="$AGENT_GID" --init-groups \
        env HOME="$AGENT_HOME" USER="$AGENT_USER" LOGNAME="$AGENT_USER" "$@"
}

# The workspace is a bind mount from the host, so its ownership is whatever the
# host uses. Claim it for `agent` when we can; a read-only or root-owned mount is
# not fatal, so only warn.
mkdir -p "$WORKSPACE" "$TERMINALFS_MNT" "$DOTNETDOCFS_MNT"
chown "$AGENT_UID:$AGENT_GID" "$AGENT_HOME/mnt" "$TERMINALFS_MNT" "$DOTNETDOCFS_MNT"
if ! chown "$AGENT_UID:$AGENT_GID" "$WORKSPACE" 2>/dev/null; then
    warn "could not chown $WORKSPACE; the agent user may not be able to write to it"
fi

# Agent state lives under $AGENT_HOME/.credentials, which run.sh backs with a
# named volume so logins survive `docker rm`. Claude Code is pointed at it with
# CLAUDE_CONFIG_DIR and opencode through a symlink baked into the image; both
# work without the volume too, they just stop persisting.
CRED_DIR="$AGENT_HOME/.credentials"
CLAUDE_CONFIG_DIR="${CLAUDE_CONFIG_DIR:-$CRED_DIR/claude}"
export CLAUDE_CONFIG_DIR
mkdir -p "$CLAUDE_CONFIG_DIR" "$CRED_DIR/opencode"
chown -R "$AGENT_UID:$AGENT_GID" "$CRED_DIR"

wait_for_port() {
    local port="$1" name="$2" i
    for i in $(seq 1 100); do
        if (exec 3<>"/dev/tcp/127.0.0.1/$port") 2>/dev/null; then
            exec 3>&- 3<&-
            return 0
        fi
        sleep 0.1
    done
    warn "$name did not start listening on port $port"
    return 1
}

log "starting terminalfs on 127.0.0.1:$TERMINALFS_PORT (cwd $WORKSPACE)"
as_agent terminalfs --port "$TERMINALFS_PORT" --cwd "$WORKSPACE" &
TERMINALFS_PID=$!

log "starting dotnetdocfs on 127.0.0.1:$DOTNETDOCFS_PORT"
as_agent dotnetdoc --port "$DOTNETDOCFS_PORT" &
DOTNETDOCFS_PID=$!

shutdown() {
    log "shutting down"
    umount "$TERMINALFS_MNT" 2>/dev/null || true
    umount "$DOTNETDOCFS_MNT" 2>/dev/null || true
    kill "$TERMINALFS_PID" "$DOTNETDOCFS_PID" 2>/dev/null || true
}
trap shutdown EXIT INT TERM

wait_for_port "$TERMINALFS_PORT" terminalfs || true
wait_for_port "$DOTNETDOCFS_PORT" dotnetdocfs || true

# These are the options the tools use for their own `--mount`, plus a uid/gid
# default so the unprivileged agent owns what it sees through the mount.
mount_9p() {
    local port="$1" target="$2" name="$3"
    if mount -t 9p \
        -o "trans=tcp,port=$port,version=9p2000.L,msize=262144,cache=none,access=any,dfltuid=$AGENT_UID,dfltgid=$AGENT_GID" \
        127.0.0.1 "$target" 2>&1
    then
        log "$name mounted at $target"
        return 0
    fi
    warn "$name failed to mount at $target; run the container with --cap-add SYS_ADMIN"
    return 1
}

mount_9p "$TERMINALFS_PORT" "$TERMINALFS_MNT" terminalfs || true
mount_9p "$DOTNETDOCFS_PORT" "$DOTNETDOCFS_MNT" dotnetdocfs || true

# Both servers generate their agent skill at runtime and serve it over the mount.
# opencode reads them straight off the mount (see skills.paths in opencode.json)
# and shows the model each skill's location, so it can work out where the tree
# is. Claude Code only discovers skills under its own config directory, and
# nothing substitutes the literal `<mount>` placeholder the servers write, so
# copy them across with the real path filled in. The config directory is a
# volume, so this has to happen on every start or a stale copy would win.
install_skill() {
    local src="$1" mount="$2" name="$3"
    local dest="$CLAUDE_CONFIG_DIR/skills/$name"
    [ -r "$src" ] || { warn "no skill at $src"; return 1; }
    as_agent mkdir -p "$dest"
    sed "s|<mount>|$mount|g" "$src" | as_agent tee "$dest/SKILL.md" >/dev/null
    log "installed Claude Code skill '$name'"
}

install_skill "$TERMINALFS_MNT/skills/terminalfs/SKILL.md" "$TERMINALFS_MNT" terminalfs || true
install_skill "$DOTNETDOCFS_MNT/skills/dotnet-api-docs/SKILL.md" "$DOTNETDOCFS_MNT" dotnet-api-docs || true

log "starting code-server on 0.0.0.0:$CODE_SERVER_PORT"
exec setpriv --reuid="$AGENT_UID" --regid="$AGENT_GID" --init-groups \
    env HOME="$AGENT_HOME" USER="$AGENT_USER" \
        PASSWORD="$CODE_SERVER_PASSWORD" \
        CLAUDE_CONFIG_DIR="$CLAUDE_CONFIG_DIR" \
    code-server \
        --bind-addr "0.0.0.0:$CODE_SERVER_PORT" \
        --auth password \
        --disable-telemetry \
        --disable-update-check \
        "$WORKSPACE"

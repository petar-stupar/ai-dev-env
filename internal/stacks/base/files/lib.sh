#!/bin/bash
# Shared helpers for /etc/aide/entrypoint.sh and the entrypoint.d hooks.
# Sourced, never executed; callers set their own shell options.

# Everything here runs as root, and the image's PATH includes directories
# agent can write to. Root looks commands up in root-owned directories only;
# as_agent hands the image's PATH back to whatever it runs. /usr/local/sbin is
# left out on purpose: it holds the restricted mount wrappers meant for sudo.
if [ -z "${AIDE_AGENT_PATH:-}" ]; then
    AIDE_AGENT_PATH="$PATH"
fi
export AIDE_AGENT_PATH
PATH=/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
export PATH

AGENT_USER=agent
AGENT_HOME=/home/agent
AGENT_UID="$(id -u "$AGENT_USER")"
AGENT_GID="$(id -g "$AGENT_USER")"
AIDE_CREDS="$AGENT_HOME/.credentials"
AIDE_CACHE=/var/cache/aide
AIDE_STACKS="${AIDE_STACKS:-$(tr '\n' ' ' </etc/aide/stacks 2>/dev/null | sed 's/ *$//')}"
WORKSPACE="${WORKSPACE:-$AGENT_HOME/workspace}"
export AGENT_USER AGENT_HOME AGENT_UID AGENT_GID AIDE_CREDS AIDE_CACHE AIDE_STACKS WORKSPACE

_aide_tag="[${AIDE_LOG_TAG:-$(basename "$0" .sh)}]"
log() { printf '%s %s\n' "$_aide_tag" "$*"; }
warn() { printf '%s %s\n' "$_aide_tag WARNING:" "$*" >&2; }

# setpriv changes the credentials but not the environment, and root's HOME
# would send every tool looking for its config in the wrong place.
as_agent() {
    setpriv --reuid="$AGENT_UID" --regid="$AGENT_GID" --init-groups \
        env -u AIDE_AGENT_PATH PATH="$AIDE_AGENT_PATH" \
            HOME="$AGENT_HOME" USER="$AGENT_USER" LOGNAME="$AGENT_USER" \
            XDG_CACHE_HOME="${XDG_CACHE_HOME:-$AIDE_CACHE/xdg}" "$@"
}

# agent_has CMD: whether agent finds CMD on its PATH.
agent_has() {
    # shellcheck disable=SC2016 # $1 is the inner shell's argument
    as_agent sh -c 'command -v "$1" >/dev/null 2>&1' sh "$1"
}

# has_stack NAME: whether NAME was built into this image.
has_stack() {
    grep -qxF -- "$1" /etc/aide/stacks 2>/dev/null
}

# wait_for_mount DIR [SECONDS]: wait until DIR is a mountpoint (default 10s).
wait_for_mount() {
    local dir="$1" tries=$(( ${2:-10} * 10 )) i
    for (( i = 0; i < tries; i++ )); do
        mountpoint -q "$dir" && return 0
        sleep 0.1
    done
    warn "$dir did not get mounted"
    return 1
}

# install_skill SRC MOUNT NAME: copy a SKILL.md served by a 9P tool into each
# selected agent's skill directory, with the literal `<mount>` placeholder the
# servers write filled in. The destinations live on the creds volume or in the
# image, so this runs on every start or a stale copy would win.
install_skill() {
    local src="$1" mount="$2" name="$3" dest dests=()
    [ -r "$src" ] || { warn "no skill at $src"; return 1; }
    if has_stack claude; then
        dests+=("${CLAUDE_CONFIG_DIR:-$AIDE_CREDS/claude}/skills/$name")
    fi
    if has_stack opencode || has_stack opencode-filesystem; then
        dests+=("$AGENT_HOME/.config/opencode/skills/$name")
    fi
    for dest in "${dests[@]}"; do
        if as_agent mkdir -p "$dest" \
            && sed "s|<mount>|$mount|g" "$src" | as_agent tee "$dest/SKILL.md" >/dev/null; then
            log "installed skill '$name' into $dest"
        else
            warn "could not install skill '$name' into $dest"
        fi
    done
}

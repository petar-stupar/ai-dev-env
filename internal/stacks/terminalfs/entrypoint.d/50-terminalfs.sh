#!/bin/bash
# terminalfs at start: a clean sessions root, and its plugin registered with
# whichever agents this image carries. Each step warns and carries on.
set -u
# shellcheck source=/dev/null
. /etc/aide/lib.sh

runtime="${TERMINALFS_RUNTIME_DIR:-$AGENT_HOME/mnt/terminalfs-sessions}"
root="$runtime/terminalfs"

# terminalfs refuses a runtime dir that is not agent-owned with mode 0700.
install -d -o "$AGENT_UID" -g "$AGENT_GID" -m 0700 "$runtime" \
    || warn "could not create $runtime"

# Session servers die with the container, so any session dir present now is
# left over from a stop or a snapshot commit. Detach a mount that somehow
# survived, then remove the dirs; --one-file-system keeps rm off anything
# still mounted.
if [ -d "$root" ]; then
    n=0
    for d in "$root"/*/; do
        [ -d "$d" ] || continue
        d="${d%/}"
        n=$((n + 1))
        if mountpoint -q "$d"; then
            umount -l "$d" || warn "could not unmount stale session $d"
        fi
    done
    if find "$root" -mindepth 1 -maxdepth 1 -exec rm -rf --one-file-system {} +; then
        [ "$n" -gt 0 ] && log "cleared $n stale session dir(s) under $root"
    else
        warn "could not clear stale sessions under $root"
    fi
fi

# Claude Code: the plugin (skill + SessionStart/PreToolUse hooks) replaces the
# skill copy ai-dev-env used to install. Plugin state lives under
# CLAUDE_CONFIG_DIR on the credentials volume, so it is registered here, once
# per terminalfs version (the stamp), rather than at build time.
if has_stack claude; then
    export CLAUDE_CONFIG_DIR="${CLAUDE_CONFIG_DIR:-$AIDE_CREDS/claude}"
    if [ -d "$CLAUDE_CONFIG_DIR/skills/terminalfs" ]; then
        rm -rf "$CLAUDE_CONFIG_DIR/skills/terminalfs" \
            && log "removed the old terminalfs skill copy (the plugin ships its own)"
    fi

    mp="$AGENT_HOME/.local/share/terminalfs/claude-code"
    stamp="$CLAUDE_CONFIG_DIR/.aide-terminalfs-plugin"
    v="$(terminalfs --version 2>/dev/null | head -n 1)"
    prev="$(cat "$stamp" 2>/dev/null || true)"

    if [ -z "$v" ]; then
        warn "terminalfs --version failed; not registering the Claude Code plugin"
    elif ! command -v claude >/dev/null 2>&1; then
        warn "claude not on PATH; not registering the terminalfs plugin"
    elif [ "$prev" = "$v" ]; then
        log "Claude Code plugin already registered for $v"
    else
        ok=1
        # Rewritten in case the image's copy is missing; cheap and offline.
        as_agent terminalfs plugin install claude --dir "$mp" >/dev/null \
            || { warn "terminalfs plugin install claude failed"; ok=0; }
        # add fails when the marketplace is already known: refresh it instead.
        if [ "$ok" = 1 ] && ! as_agent claude plugin marketplace add "$mp" >/dev/null 2>&1; then
            as_agent claude plugin marketplace update terminalfs >/dev/null \
                || { warn "could not add or update the terminalfs marketplace"; ok=0; }
        fi
        # First time: install. After an upgrade: update. Each falls back to the other.
        if [ "$ok" = 1 ]; then
            if [ -z "$prev" ]; then
                as_agent claude plugin install terminalfs@terminalfs >/dev/null \
                    || as_agent claude plugin update terminalfs@terminalfs >/dev/null \
                    || { warn "could not install the terminalfs Claude Code plugin"; ok=0; }
            else
                as_agent claude plugin update terminalfs@terminalfs >/dev/null \
                    || as_agent claude plugin install terminalfs@terminalfs >/dev/null \
                    || { warn "could not update the terminalfs Claude Code plugin"; ok=0; }
            fi
        fi
        if [ "$ok" = 1 ]; then
            printf '%s\n' "$v" | as_agent tee "$stamp" >/dev/null
            log "Claude Code plugin registered for $v${prev:+ (was $prev)}"
        fi
    fi
fi

# opencode: the plugin lives in the image's ~/.config/opencode/plugins, where
# opencode discovers it. Rewriting it each start is cheap and keeps it in step
# with the binary.
if has_stack opencode-filesystem; then
    if as_agent terminalfs plugin install opencode >/dev/null; then
        log "opencode plugin installed"
    else
        warn "terminalfs plugin install opencode failed"
    fi
fi

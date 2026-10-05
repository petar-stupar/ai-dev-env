#!/bin/bash
# terminalfs at start: a clean sessions root, and its plugin registered with
# whichever agents this image carries. Each step warns and carries on, except
# that Claude Code loses Bash when its plugin cannot be registered.
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
# survived, then remove the dirs. Both directories belong to agent, so
# neither is followed if it has become a symlink, and the removal runs as
# agent; --one-file-system keeps rm off anything still mounted.
if [ -L "$runtime" ] || [ -L "$root" ]; then
    warn "$root is reached through a symlink; not clearing stale sessions"
elif [ -d "$root" ]; then
    n=0
    for d in "$root"/*/; do
        [ -d "$d" ] || continue
        d="${d%/}"
        n=$((n + 1))
        if mountpoint -q "$d"; then
            umount -l "$d" || warn "could not unmount stale session $d"
        fi
    done
    if as_agent find "$root" -mindepth 1 -maxdepth 1 -exec rm -rf --one-file-system {} +; then
        [ "$n" -gt 0 ] && log "cleared $n stale session dir(s) under $root"
    else
        warn "could not clear stale sessions under $root"
    fi
fi

# Claude Code: the plugin (skill + SessionStart/PreToolUse hooks) is what keeps
# Bash inside the tree; the managed policy allows Bash on that understanding.
# The policy also names the plugin as enabled and this image's root-owned
# marketplace dir as its source. The installed copy and its registration live
# under CLAUDE_CONFIG_DIR on the credentials volume, which agent can write
# to, so they are put back from the image at every start rather than trusted,
# and the result is checked. If the plugin is not there afterwards, Bash is
# switched off for Claude Code through a managed drop-in until a start
# succeeds: the container still comes up, but not with Bash unguarded.
if has_stack claude; then
    export CLAUDE_CONFIG_DIR="${CLAUDE_CONFIG_DIR:-$AIDE_CREDS/claude}"
    if [ -d "$CLAUDE_CONFIG_DIR/skills/terminalfs" ]; then
        as_agent rm -rf "$CLAUDE_CONFIG_DIR/skills/terminalfs" \
            && log "removed the old terminalfs skill copy (the plugin ships its own)"
    fi

    mp=/usr/local/share/terminalfs/claude-code
    plugin=terminalfs@terminalfs
    dropins=/etc/claude-code/managed-settings.d
    bash_off="$dropins/90-aide-terminalfs-plugin-missing.json"

    # plugin_registered: whether Claude Code lists the plugin, enabled.
    plugin_registered() {
        local out
        if out="$(as_agent claude plugin list --json 2>/dev/null)"; then
            printf '%s' "$out" | jq -e --arg id "$plugin" \
                '[.. | objects | select((.id? // .name? // "") == $id)] | any(.enabled != false)' \
                >/dev/null 2>&1
        else
            # An older CLI without --json: fall back to its registry file.
            jq -e --arg id "$plugin" '.plugins | has($id)' \
                "$CLAUDE_CONFIG_DIR/plugins/installed_plugins.json" >/dev/null 2>&1
        fi
    }

    if ! agent_has claude; then
        warn "claude not on PATH; cannot register the terminalfs plugin"
    elif [ ! -d "$mp" ]; then
        warn "$mp is missing; cannot register the terminalfs plugin"
    else
        # add fails when the marketplace is already known: refresh it instead.
        as_agent claude plugin marketplace add "$mp" >/dev/null 2>&1 \
            || as_agent claude plugin marketplace update terminalfs >/dev/null 2>&1 \
            || warn "could not add or update the terminalfs marketplace"
        # Dropped and installed again so the copy that runs is the image's.
        as_agent claude plugin uninstall "$plugin" >/dev/null 2>&1 || true
        as_agent claude plugin install "$plugin" >/dev/null 2>&1 \
            || as_agent claude plugin update "$plugin" >/dev/null 2>&1 \
            || warn "could not install the terminalfs Claude Code plugin"
    fi

    if plugin_registered; then
        rm -f "$bash_off"
        log "Claude Code plugin registered ($(terminalfs --version 2>/dev/null | head -n 1))"
    else
        install -d -m 0755 "$dropins"
        printf '{"permissions":{"deny":["Bash"]}}\n' >"$bash_off"
        chmod 0644 "$bash_off"
        warn "the terminalfs Claude Code plugin is not registered, so nothing would keep Bash inside the tree; Bash is turned off for Claude Code until a container start registers it"
    fi
    rm -f "$CLAUDE_CONFIG_DIR/.aide-terminalfs-plugin" # the stamp older images kept
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

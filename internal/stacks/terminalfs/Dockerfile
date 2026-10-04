# Release to install: latest or an exact vX.Y.Z. The upstream install script
# picks the self-contained linux x64/arm64 build.
ARG TERMINALFS_VERSION=v0.5.0
RUN curl -fsSL https://raw.githubusercontent.com/petar-stupar/terminalfs/main/scripts/install.sh \
        | sh -s -- --version "${TERMINALFS_VERSION}" --bin-dir /usr/local/bin \
    && terminalfs --version

# Each agent session gets its own 9P tree under $TERMINALFS_RUNTIME_DIR/terminalfs/<id>.
# terminalfs insists on an absolute, agent-owned, 0700 directory. Strict mode
# makes the Claude Code hook deny Bash that bypasses the tree; it is set here
# and again in the managed settings env so both the hook and any shell see it.
ENV TERMINALFS_RUNTIME_DIR=/home/agent/mnt/terminalfs-sessions \
    TERMINALFS_CLAUDE_STRICT=1
RUN install -d -o agent -g agent -m 0700 /home/agent/mnt/terminalfs-sessions

USER agent

# The settings file is required at runtime; write the defaults into the image.
# The Claude Code marketplace dir is baked in too, so registering the plugin at
# start (its state lives on the credentials volume) needs no network.
RUN mkdir -p /home/agent/.local/share/terminalfs \
    && terminalfs --init-settings \
    && test -s /home/agent/.config/terminalfs/settings.json \
    && terminalfs plugin install claude --dir /home/agent/.local/share/terminalfs/claude-code

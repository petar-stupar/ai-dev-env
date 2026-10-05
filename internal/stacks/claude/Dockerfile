# Version for the official installer: stable, latest or an exact X.Y.Z.
ARG CLAUDE_VERSION=stable

# Everything Claude Code writes (credentials, .claude.json, session history,
# plugins) goes to the credentials volume, so a login survives a rebuild.
ENV CLAUDE_CONFIG_DIR=/home/agent/.credentials/claude

USER agent

# The installer its docs recommend; lands in ~/.local/bin (a symlink into
# ~/.local/share/claude/versions/).
RUN curl -fsSL https://claude.ai/install.sh | bash -s -- "${CLAUDE_VERSION}" \
    && ~/.local/bin/claude --version

# The VS Code extension, from Open VSX (code-server's marketplace).
RUN code-server --install-extension Anthropic.claude-code \
    && code-server --list-extensions | grep -qi '^anthropic.claude-code$'

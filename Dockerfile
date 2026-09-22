# An isolated AI development environment: VS Code Server, the 9P-filesystem
# build of opencode, Claude Code, terminalfs and dotnetdocfs, on the .NET 10 SDK.
#
# Build with ./build.sh, run with ./run.sh. Builds for the host architecture;
# linux/amd64 and linux/arm64 (Apple Silicon) are both supported.
FROM mcr.microsoft.com/dotnet/sdk:10.0

# Set by BuildKit. Used to pick the right release asset.
ARG TARGETARCH

ENV DEBIAN_FRONTEND=noninteractive
ENV LANG=C.UTF-8

# mount/mountpoint (util-linux) mount the 9P filesystems and setpriv drops
# privileges in the entrypoint. ripgrep is used by both agents' search tools;
# without a system rg they each download their own copy on first use.
RUN apt-get update && apt-get install -y --no-install-recommends \
        build-essential \
        ca-certificates \
        curl \
        file \
        git \
        gnupg \
        htop \
        jq \
        less \
        libsecret-1-0 \
        locales \
        mount \
        nano \
        openssh-client \
        pkg-config \
        procps \
        python3 \
        python3-pip \
        python3-venv \
        ripgrep \
        rsync \
        sudo \
        tar \
        tmux \
        unzip \
        util-linux \
        vim-tiny \
        wget \
        xz-utils \
        zip \
    && rm -rf /var/lib/apt/lists/*

# Node.js LTS, which brings npm and npx with it.
RUN curl -fsSL https://deb.nodesource.com/setup_lts.x | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && rm -rf /var/lib/apt/lists/* \
    && npm install -g npm@latest

# VS Code Server. The official script picks the right architecture.
RUN curl -fsSL https://code-server.dev/install.sh | sh \
    && rm -rf /root/.cache

# terminalfs and dotnetdocfs, installed the way their READMEs describe. Their
# install scripts default to ~/.local/bin; point them somewhere on the global
# PATH because the entrypoint starts them as a different user than it builds as.
ARG TERMINALFS_VERSION=latest
ARG DOTNETDOCFS_VERSION=latest
RUN curl -fsSL https://raw.githubusercontent.com/petar-stupar/terminalfs/main/scripts/install.sh \
        | sh -s -- --version "$TERMINALFS_VERSION" --bin-dir /usr/local/bin \
    && terminalfs --version
RUN curl -fsSL https://raw.githubusercontent.com/petar-stupar/dotnetdocfs/main/scripts/install.sh \
        | sh -s -- --version "$DOTNETDOCFS_VERSION" --bin-dir /usr/local/bin \
    && dotnetdoc --version

# The 9P-filesystem build of opencode, from the fork's rolling prerelease.
ARG OPENCODE_FS_REPO=petar-stupar/opencode
ARG OPENCODE_FS_TAG=filesystem-latest
RUN set -eux; \
    case "$TARGETARCH" in \
        amd64) asset=opencode-linux-x64.tar.gz ;; \
        arm64) asset=opencode-linux-arm64.tar.gz ;; \
        *) echo "unsupported architecture: $TARGETARCH" >&2; exit 1 ;; \
    esac; \
    curl -fsSL -o /tmp/opencode.tar.gz \
        "https://github.com/${OPENCODE_FS_REPO}/releases/download/${OPENCODE_FS_TAG}/${asset}"; \
    mkdir -p /tmp/opencode /opt/opencode-fs; \
    tar -xzf /tmp/opencode.tar.gz -C /tmp/opencode; \
    install -m 0755 "$(find /tmp/opencode -type f -name opencode -perm -u+x | head -1)" /opt/opencode-fs/opencode; \
    rm -rf /tmp/opencode /tmp/opencode.tar.gz
ENV PATH="/opt/opencode-fs:/home/agent/.local/bin:${PATH}"
RUN opencode --version

# The unprivileged user everything runs as. Default it to 1000 so bind-mounted
# workspaces line up with the usual first user on a Linux host; override
# AGENT_UID/AGENT_GID at build time if yours differs. The base image already
# ships an `ubuntu` user at 1000, so that has to go first.
ARG AGENT_UID=1000
ARG AGENT_GID=1000
RUN set -eux; \
    if existing="$(getent passwd "$AGENT_UID" | cut -d: -f1)" && [ -n "$existing" ]; then \
        userdel --remove "$existing"; \
    fi; \
    if existing="$(getent group "$AGENT_GID" | cut -d: -f1)" && [ -n "$existing" ]; then \
        groupdel "$existing"; \
    fi; \
    groupadd --gid "$AGENT_GID" agent; \
    useradd --create-home --shell /bin/bash --uid "$AGENT_UID" --gid "$AGENT_GID" agent; \
    echo "agent:agent" | chpasswd; \
    echo "agent ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/agent; \
    chmod 0440 /etc/sudoers.d/agent; \
    mkdir -p /home/agent/workspace /home/agent/mnt/terminalfs /home/agent/mnt/dotnetdocfs; \
    chown -R agent:agent /home/agent

# Claude Code's managed settings. This tier outranks every user, project and
# command-line setting, so the denied tools cannot be turned back on from inside
# a session. See config/claude/managed-settings.json for what it denies and why.
RUN mkdir -p /etc/claude-code
COPY config/claude/managed-settings.json /etc/claude-code/managed-settings.json

# Everything Claude Code writes -- credentials, .claude.json, session history --
# goes here, and run.sh mounts a named volume over it so a login survives
# `docker rm`. Relocating .claude.json is the documented reason to set this.
ENV CLAUDE_CONFIG_DIR=/home/agent/.credentials/claude

USER agent
WORKDIR /home/agent

# Claude Code, via the installer its docs recommend. Lands in ~/.local/bin.
RUN curl -fsSL https://claude.ai/install.sh | bash \
    && ~/.local/bin/claude --version

# opencode keeps auth.json in its XDG data directory. Point that at the same
# volume. Moving XDG_DATA_HOME wholesale would drag code-server's extensions
# along with it, so link just this one directory.
RUN mkdir -p /home/agent/.credentials/opencode /home/agent/.local/share \
    && ln -sfn /home/agent/.credentials/opencode /home/agent/.local/share/opencode

# opencode's global config: where the 9P skills are, and unconditional access to
# the two mounts. Not on the volume, so it tracks the image.
COPY --chown=agent:agent config/opencode/opencode.json /home/agent/.config/opencode/opencode.json

# terminalfs refuses to start without a deny list, so generate the default one.
RUN terminalfs --init-settings \
    && test -s /home/agent/.config/terminalfs/settings.json

# VS Code extensions, from Open VSX (code-server's marketplace).
RUN code-server --install-extension Anthropic.claude-code \
    && code-server --list-extensions | grep -qi '^anthropic.claude-code$'

USER root
COPY scripts/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod 0755 /usr/local/bin/entrypoint.sh

ENV WORKSPACE=/home/agent/workspace \
    CODE_SERVER_PORT=8080 \
    TERMINALFS_PORT=15641 \
    DOTNETDOCFS_PORT=15640

WORKDIR /home/agent/workspace
EXPOSE 8080

# Runs as root to mount 9P, then drops to `agent` for everything else.
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]

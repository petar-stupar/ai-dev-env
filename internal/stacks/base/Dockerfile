# mount/mountpoint (util-linux) mount the 9P filesystems and setpriv drops
# privileges in the entrypoint. ripgrep is used by both agents' search tools;
# without a system rg they each download their own copy on first use. xz-utils
# unpacks the nodejs.org tarballs.
RUN apt-get update && apt-get install -y --no-install-recommends \
        bash \
        build-essential \
        ca-certificates \
        curl \
        file \
        git \
        gnupg \
        gzip \
        htop \
        jq \
        less \
        libsecret-1-0 \
        locales \
        mount \
        nano \
        neovim \
        openssh-client \
        pkg-config \
        procps \
        ripgrep \
        rsync \
        sudo \
        tar \
        tmux \
        unzip \
        util-linux \
        wget \
        xz-utils \
        zip \
    && rm -rf /var/lib/apt/lists/*

# VS Code Server. The official script picks the .deb for the architecture.
# Unpinned by default; pass --build-arg CODE_SERVER_VERSION=x.y.z to pin.
ARG CODE_SERVER_VERSION=
RUN if [ -n "$CODE_SERVER_VERSION" ]; then set -- --version "$CODE_SERVER_VERSION"; fi; \
    curl -fsSL https://code-server.dev/install.sh | sh -s -- "$@" \
    && rm -rf /root/.cache \
    && code-server --version

# The unprivileged user everything runs as. Default it to 1000 so bind-mounted
# workspaces line up with the usual first user on a Linux host; aide passes the
# host's uid/gid. The base image already ships an `ubuntu` user at 1000 (and on
# macOS gid 20 is `dialout`), so whatever holds the ids has to go first.
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
    mkdir -p /home/agent/workspace /home/agent/mnt /home/agent/.local/bin \
        /home/agent/.config /home/agent/.cache /var/cache/aide/xdg \
        /etc/aide /usr/local/lib/aide; \
    chown -R agent:agent /home/agent /var/cache/aide

# The runtime every stack's entrypoint.d hook builds on.
COPY --chmod=0755 stacks/base/files/entrypoint.sh stacks/base/files/lib.sh /etc/aide/
COPY --chmod=0755 stacks/base/files/install-node.sh /usr/local/lib/aide/

# /opt/node/current is linked by the common tail when a node stack is selected.
# Caches go to the cache volume so they stay out of snapshots.
ENV PATH=/home/agent/.local/bin:/opt/node/current/bin:$PATH \
    XDG_CACHE_HOME=/var/cache/aide/xdg

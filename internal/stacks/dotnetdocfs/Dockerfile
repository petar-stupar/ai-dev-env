# Release to install: latest or an exact vX.Y.Z. The upstream install script
# picks the self-contained linux x64/arm64 build of `dotnetdoc`.
ARG DOTNETDOCFS_VERSION=latest
RUN curl -fsSL https://raw.githubusercontent.com/petar-stupar/dotnetdocfs/main/scripts/install.sh \
        | sh -s -- --version "${DOTNETDOCFS_VERSION}" --bin-dir /usr/local/bin \
    && dotnetdoc --version

# The mountpoint must exist and belong to agent, who runs the server and mounts
# it (through the mount wrapper sudo allows it) at start. The port is loopback-only.
RUN install -d -o agent -g agent /home/agent/mnt/dotnetdocfs
ENV DOTNETDOCFS_PORT=15640

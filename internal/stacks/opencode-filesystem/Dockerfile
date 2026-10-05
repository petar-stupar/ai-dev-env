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
    install -m 0755 "$(find /tmp/opencode -type f -path '*/bin/opencode' | head -1)" /opt/opencode-fs/opencode; \
    rm -rf /tmp/opencode /tmp/opencode.tar.gz
ENV PATH=/opt/opencode-fs:$PATH
RUN opencode --version

USER agent

# opencode keeps its credentials (SQLite) in $XDG_DATA_HOME/opencode. Link just
# that directory to the credentials volume; moving XDG_DATA_HOME wholesale would
# drag code-server's extensions along.
RUN mkdir -p ~/.credentials/opencode ~/.local/share \
    && ln -sfn /home/agent/.credentials/opencode /home/agent/.local/share/opencode

# Empty installs the latest release; otherwise an exact version.
ARG OPENCODE_VERSION=

USER agent

# The upstream installer always installs into ~/.opencode/bin.
RUN if [ -n "${OPENCODE_VERSION}" ]; then \
        curl -fsSL https://opencode.ai/install | bash -s -- --no-modify-path --version "${OPENCODE_VERSION}"; \
    else \
        curl -fsSL https://opencode.ai/install | bash -s -- --no-modify-path; \
    fi
ENV PATH=$PATH:/home/agent/.opencode/bin

# opencode keeps its credentials (SQLite) in $XDG_DATA_HOME/opencode. Link just
# that directory to the credentials volume; moving XDG_DATA_HOME wholesale would
# drag code-server's extensions along.
RUN mkdir -p ~/.credentials/opencode ~/.local/share \
    && ln -sfn /home/agent/.credentials/opencode /home/agent/.local/share/opencode

RUN opencode --version

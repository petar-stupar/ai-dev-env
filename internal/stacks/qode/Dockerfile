# The installer hardcodes $HOME/.local/bin and needs no root, so run it as agent (the engine restores USER root after).
USER agent
RUN curl -sSfL https://raw.githubusercontent.com/nqode-io/qode/main/install.sh | sh
RUN /home/agent/.local/bin/qode --version

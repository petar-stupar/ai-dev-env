# Ubuntu's golang package lags far behind, so install the official tarball, checksum-verified
# against go.dev's own release metadata. GO_VERSION=latest tracks current stable.
ARG GO_VERSION=latest
ARG TARGETARCH
RUN set -eu; \
    json=$(curl -fsSL 'https://go.dev/dl/?mode=json'); \
    if [ "$GO_VERSION" = latest ]; then \
      ver=$(printf '%s' "$json" | jq -r '.[0].version'); \
    else \
      ver=go${GO_VERSION#go}; \
    fi; \
    file=$(printf '%s' "$json" | jq -r --arg v "$ver" --arg a "$TARGETARCH" \
      '.[] | select(.version==$v) | .files[] | select(.kind=="archive" and .os=="linux" and .arch==$a) | .filename' | head -n1); \
    sha=$(printf '%s' "$json" | jq -r --arg f "$file" '.[].files[] | select(.filename==$f) | .sha256' | head -n1); \
    [ -n "$file" ] && [ -n "$sha" ] || { echo "no linux/$TARGETARCH archive for $ver" >&2; exit 1; }; \
    curl -fsSL -o "/tmp/$file" "https://go.dev/dl/$file"; \
    echo "$sha  /tmp/$file" | sha256sum -c -; \
    tar -C /usr/local -xzf "/tmp/$file"; \
    rm -f "/tmp/$file"
# Caches live on the cache volume; -buildvcs=false because workspaces are often not git roots.
ENV PATH=/usr/local/go/bin:/home/agent/go/bin:$PATH \
    GOMODCACHE=/var/cache/aide/gomod \
    GOCACHE=/var/cache/aide/gobuild \
    GOFLAGS=-buildvcs=false
RUN go version

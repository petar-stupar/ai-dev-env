#!/usr/bin/env bash
# Builds the ai-dev-env image for the host architecture.
set -euo pipefail
cd "$(dirname "$0")"

IMAGE="${AI_DEV_ENV_IMAGE:-ai-dev-env}"
TAG="${AI_DEV_ENV_TAG:-latest}"
NO_CACHE=""
PLATFORM=""
BUILD_ARGS=()

usage() {
    cat <<'EOF'
usage: ./build.sh [options]

  --image NAME          image name; ai-dev-env by default
  --tag TAG             image tag; latest by default
  --platform PLATFORM   linux/arm64 or linux/amd64; the host's by default
  --no-cache            rebuild every layer
  --opencode-tag TAG    release tag to take opencode from; filesystem-latest by default
  --terminalfs VERSION  terminalfs release; latest by default
  --dotnetdocfs VERSION dotnetdocfs release; latest by default
  -h, --help            print this

The agents and their tooling are pulled from the network at build time, so
--no-cache is how you pick up new releases of any of them.
EOF
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --image) IMAGE="$2"; shift 2 ;;
        --tag) TAG="$2"; shift 2 ;;
        --platform) PLATFORM="$2"; shift 2 ;;
        --no-cache) NO_CACHE="--no-cache"; shift ;;
        --opencode-tag) BUILD_ARGS+=(--build-arg "OPENCODE_FS_TAG=$2"); shift 2 ;;
        --terminalfs) BUILD_ARGS+=(--build-arg "TERMINALFS_VERSION=$2"); shift 2 ;;
        --dotnetdocfs) BUILD_ARGS+=(--build-arg "DOTNETDOCFS_VERSION=$2"); shift 2 ;;
        -h|--help) usage; exit 0 ;;
        *) echo "unknown option: $1" >&2; usage >&2; exit 2 ;;
    esac
done

if [[ -n "$PLATFORM" ]]; then
    BUILD_ARGS+=(--platform "$PLATFORM")
fi

echo "Building $IMAGE:$TAG${PLATFORM:+ for $PLATFORM}"
# The ${a[@]+"${a[@]}"} form is needed because macOS ships bash 3.2, where
# expanding an empty array under `set -u` is an error.
# shellcheck disable=SC2086
docker build $NO_CACHE ${BUILD_ARGS[@]+"${BUILD_ARGS[@]}"} -t "$IMAGE:$TAG" .

echo
echo "Built $IMAGE:$TAG"
docker run --rm --entrypoint sh "$IMAGE:$TAG" -c '
    printf "  dotnet       %s\n" "$(dotnet --version)"
    printf "  node         %s\n" "$(node --version)"
    printf "  npm          %s\n" "$(npm --version)"
    printf "  python3      %s\n" "$(python3 --version | cut -d" " -f2)"
    printf "  code-server  %s\n" "$(code-server --version 2>/dev/null | grep -oE "^[0-9]+\.[0-9]+\.[0-9]+" | head -1)"
    printf "  opencode     %s\n" "$(opencode --version)"
    printf "  claude       %s\n" "$(/home/agent/.local/bin/claude --version)"
    printf "  terminalfs   %s\n" "$(terminalfs --version | head -1 | cut -d" " -f2)"
    printf "  dotnetdoc    %s\n" "$(dotnetdoc --version | head -1 | cut -d" " -f2)"
'
echo
echo "Start it in a project with:  ./run.sh /path/to/project"

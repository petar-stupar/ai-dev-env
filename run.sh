#!/usr/bin/env bash
# Starts an ai-dev-env container for one project directory.
#
# The directory is mounted at /home/agent/workspace. Each directory gets its own
# container, its own host port and its own credentials volume, so several can run
# at once for different projects without colliding.
set -euo pipefail

IMAGE="${AI_DEV_ENV_IMAGE:-ai-dev-env}:${AI_DEV_ENV_TAG:-latest}"
PASSWORD="${AI_DEV_ENV_PASSWORD:-agent}"
BIND_HOST="${AI_DEV_ENV_BIND:-127.0.0.1}"
PORT=""
RECREATE=""
WORKDIR=""

usage() {
    cat <<'EOF'
usage: ./run.sh [DIRECTORY] [options]

  DIRECTORY          the project to work on; the current directory by default
  --port PORT        host port for VS Code Server; the first free one from 8080 by default
  --name NAME        container name; derived from the directory by default
  --password PASS    VS Code Server password; "agent" by default
  --bind ADDRESS     host address to publish on; 127.0.0.1 by default
  --image IMAGE      image to run; ai-dev-env:latest by default
  --recreate         replace an existing container for this directory
  --stop             stop the container for this directory and exit
  --rm               stop and remove it, keeping its credentials volume
  --purge            stop and remove it and its credentials volume
  -h, --help         print this
EOF
}

ACTION=run
NAME=""
while [[ $# -gt 0 ]]; do
    case "$1" in
        --port) PORT="$2"; shift 2 ;;
        --name) NAME="$2"; shift 2 ;;
        --password) PASSWORD="$2"; shift 2 ;;
        --bind) BIND_HOST="$2"; shift 2 ;;
        --image) IMAGE="$2"; shift 2 ;;
        --recreate) RECREATE=1; shift ;;
        --stop) ACTION=stop; shift ;;
        --rm) ACTION=rm; shift ;;
        --purge) ACTION=purge; shift ;;
        -h|--help) usage; exit 0 ;;
        -*) echo "unknown option: $1" >&2; usage >&2; exit 2 ;;
        *) WORKDIR="$1"; shift ;;
    esac
done

WORKDIR="$(cd "${WORKDIR:-$PWD}" && pwd -P)"

# Name the container after the directory, with a hash of the full path appended
# so two projects with the same basename don't fight over one container.
slug="$(basename "$WORKDIR" | tr '[:upper:]' '[:lower:]' | tr -c 'a-z0-9_.-' '-' | sed 's/^-*//; s/-*$//')"
[[ -n "$slug" ]] || slug=workspace
hash="$(printf '%s' "$WORKDIR" | shasum -a 256 | cut -c1-6)"
CONTAINER="${NAME:-aidev-${slug}-${hash}}"
VOLUME="aidev-creds-${slug}-${hash}"

container_exists() { docker container inspect "$CONTAINER" >/dev/null 2>&1; }
container_running() { [[ "$(docker container inspect -f '{{.State.Running}}' "$CONTAINER" 2>/dev/null)" == "true" ]]; }
published_port() { docker port "$CONTAINER" 8080 2>/dev/null | head -1 | sed 's/.*://'; }

case "$ACTION" in
    stop)
        container_exists || { echo "No container for $WORKDIR"; exit 0; }
        docker stop "$CONTAINER" >/dev/null && echo "Stopped $CONTAINER"
        exit 0 ;;
    rm|purge)
        if container_exists; then
            docker rm -f "$CONTAINER" >/dev/null && echo "Removed $CONTAINER"
        else
            echo "No container for $WORKDIR"
        fi
        if [[ "$ACTION" == purge ]]; then
            docker volume rm "$VOLUME" >/dev/null 2>&1 \
                && echo "Removed credentials volume $VOLUME" \
                || echo "No credentials volume $VOLUME"
        fi
        exit 0 ;;
esac

docker image inspect "$IMAGE" >/dev/null 2>&1 || {
    echo "Image $IMAGE not found. Build it first with ./build.sh" >&2
    exit 1
}

if container_exists && [[ -z "$RECREATE" ]]; then
    if container_running; then
        echo "Already running for $WORKDIR"
    else
        docker start "$CONTAINER" >/dev/null
        echo "Restarted $CONTAINER"
    fi
    echo "  VS Code Server: http://${BIND_HOST}:$(published_port)"
    echo "  Password:       $PASSWORD"
    echo "  Stop with:      $0 '$WORKDIR' --stop"
    exit 0
fi

container_exists && docker rm -f "$CONTAINER" >/dev/null

if [[ -z "$PORT" ]]; then
    # First free port from 8080 that no process holds and no container publishes.
    taken="$(docker ps --format '{{.Ports}}' | grep -oE ':[0-9]+->' | tr -d ':->' || true)"
    for candidate in $(seq 8080 8199); do
        grep -qx "$candidate" <<<"$taken" && continue
        (exec 3<>"/dev/tcp/127.0.0.1/$candidate") 2>/dev/null && { exec 3>&- 3<&-; continue; }
        PORT="$candidate"
        break
    done
    [[ -n "$PORT" ]] || { echo "No free port between 8080 and 8199" >&2; exit 1; }
fi

docker volume create "$VOLUME" >/dev/null

# SYS_ADMIN is what lets the entrypoint mount the two 9P filesystems. No host
# credentials are mounted; the agents are authenticated inside the container and
# their state is kept on the named volume.
docker run --detach \
    --name "$CONTAINER" \
    --hostname "$slug" \
    --init \
    --cap-add SYS_ADMIN \
    --publish "${BIND_HOST}:${PORT}:8080" \
    --volume "$WORKDIR:/home/agent/workspace" \
    --volume "$VOLUME:/home/agent/.credentials" \
    --env "CODE_SERVER_PASSWORD=$PASSWORD" \
    "$IMAGE" >/dev/null

echo "Started $CONTAINER"
echo "  Project:        $WORKDIR"
echo "  VS Code Server: http://${BIND_HOST}:${PORT}"
echo "  Password:       $PASSWORD"
echo "  Credentials:    $VOLUME (persists across restarts)"
echo
echo "  Shell:          docker exec -it -u agent $CONTAINER bash -l"
echo "  Logs:           docker logs -f $CONTAINER"
echo "  Stop:           $0 '$WORKDIR' --stop"

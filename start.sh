#!/usr/bin/env bash
set -euo pipefail

CONTAINER_NAME="coin-server"
IMAGE="coin-server:latest"
ENV_FILE=".env"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
DOCKERFILE="$SCRIPT_DIR/deploy/docker/Dockerfile.api"

cd "$SCRIPT_DIR"

check_docker() {
    if ! command -v docker &>/dev/null; then
        echo "Error: docker is not installed" >&2
        exit 1
    fi
}

check_env() {
    if [[ ! -f "$ENV_FILE" ]]; then
        echo "Error: $ENV_FILE not found in $SCRIPT_DIR" >&2
        exit 1
    fi
}

do_build() {
    check_docker
    echo "Building image..."
    docker build -f "$DOCKERFILE" -t "$IMAGE" "$SCRIPT_DIR"
    echo "Done. Run '$0 restart' to apply the update."
}

do_start() {
    check_docker
    check_env

    if docker ps -q -f "name=^${CONTAINER_NAME}$" | grep -q .; then
        echo "Container '$CONTAINER_NAME' is already running"
        return
    fi

    if docker ps -aq -f "name=^${CONTAINER_NAME}$" | grep -q .; then
        echo "Removing stopped container '$CONTAINER_NAME'..."
        docker rm "$CONTAINER_NAME" >/dev/null
    fi

    mkdir -p logs

    echo "Starting $CONTAINER_NAME..."
    docker run -d \
        --name "$CONTAINER_NAME" \
        --network host \
        --env-file "$ENV_FILE" \
        --restart unless-stopped \
        -v "$SCRIPT_DIR/logs:/app/logs" \
        "$IMAGE"

    echo "Container '$CONTAINER_NAME' started"
}

do_stop() {
    check_docker
    if docker ps -q -f "name=^${CONTAINER_NAME}$" | grep -q .; then
        echo "Stopping $CONTAINER_NAME..."
        docker stop "$CONTAINER_NAME" >/dev/null
    fi
    if docker ps -aq -f "name=^${CONTAINER_NAME}$" | grep -q .; then
        docker rm "$CONTAINER_NAME" >/dev/null
        echo "Container '$CONTAINER_NAME' stopped and removed"
    else
        echo "Container '$CONTAINER_NAME' is not running"
    fi
}

do_restart() {
    do_stop
    do_start
}

do_logs() {
    check_docker
    docker logs -f "$CONTAINER_NAME"
}

do_status() {
    check_docker
    if docker ps -q -f "name=^${CONTAINER_NAME}$" | grep -q .; then
        docker ps -f "name=^${CONTAINER_NAME}$"
    else
        echo "Container '$CONTAINER_NAME' is not running"
    fi
}

case "${1:-start}" in
    start)   do_start   ;;
    stop)    do_stop    ;;
    restart) do_restart ;;
    logs)    do_logs    ;;
    status)  do_status  ;;
    build)   do_build   ;;
    *)
        echo "Usage: $0 {start|stop|restart|logs|status|build}" >&2
        exit 1
        ;;
esac

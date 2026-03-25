#!/usr/bin/env bash
set -euo pipefail

API_CONTAINER="coin-api"
ADMIN_CONTAINER="coin-admin-web"
API_IMAGE="coin-api:latest"
ADMIN_IMAGE="coin-admin-web:latest"
ENV_FILE=".env"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
API_DOCKERFILE="$SCRIPT_DIR/deploy/docker/Dockerfile.api"
ADMIN_DOCKERFILE="$SCRIPT_DIR/deploy/docker/Dockerfile.admin"

# Defaults (can be overridden in .env)
: "${HTTP_ADDR:=:8082}"
: "${FRONTEND_PORT:=3001}"
: "${NEXT_PUBLIC_ADMIN_API_BASE:=/admin/api/v1}"

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
    # Load env so variables are available for this script
    set -a; source "$ENV_FILE"; set +a
    : "${HTTP_ADDR:=:8082}"
    : "${FRONTEND_PORT:=3001}"
    : "${NEXT_PUBLIC_ADMIN_API_BASE:=/admin/api/v1}"
    # Extract host port from HTTP_ADDR (e.g. "127.0.0.1:8082" or ":8082" -> "8082")
    API_PORT="${HTTP_ADDR##*:}"
}

do_build() {
    check_docker
    echo "Building api image..."
    docker build -f "$API_DOCKERFILE" -t "$API_IMAGE" "$SCRIPT_DIR"

    echo "Building admin-web image..."
    docker build -f "$ADMIN_DOCKERFILE" \
        --build-arg NEXT_PUBLIC_ADMIN_API_BASE="$NEXT_PUBLIC_ADMIN_API_BASE" \
        --build-arg NEXT_ADMIN_PROXY_TARGET="http://${API_CONTAINER}:8080" \
        -t "$ADMIN_IMAGE" "$SCRIPT_DIR"

    echo "Done. Run '$0 restart' to apply the update."
}

start_container() {
    local name="$1"
    local image="$2"
    shift 2

    if docker ps -q -f "name=^${name}$" | grep -q .; then
        echo "Container '$name' is already running"
        return
    fi
    if docker ps -aq -f "name=^${name}$" | grep -q .; then
        echo "Removing stopped container '$name'..."
        docker rm "$name" >/dev/null
    fi

    docker run -d --name "$name" "$@" "$image"
    echo "Container '$name' started"
}

stop_container() {
    local name="$1"
    if docker ps -q -f "name=^${name}$" | grep -q .; then
        echo "Stopping $name..."
        docker stop "$name" >/dev/null
    fi
    if docker ps -aq -f "name=^${name}$" | grep -q .; then
        docker rm "$name" >/dev/null
        echo "Container '$name' removed"
    fi
}

do_start() {
    check_docker
    check_env

    # Ensure both images exist
    if ! docker image inspect "$API_IMAGE" &>/dev/null || ! docker image inspect "$ADMIN_IMAGE" &>/dev/null; then
        echo "Images not found, building first..."
        do_build
    fi

    # Create shared network if not exists
    docker network inspect coin-net &>/dev/null || docker network create coin-net >/dev/null

    mkdir -p logs

    echo "Starting $API_CONTAINER..."
    start_container "$API_CONTAINER" "$API_IMAGE" \
        --network coin-net \
        --env-file "$ENV_FILE" \
        --restart unless-stopped \
        -p "${API_PORT}:${API_PORT}"

    echo "Starting $ADMIN_CONTAINER..."
    start_container "$ADMIN_CONTAINER" "$ADMIN_IMAGE" \
        --network coin-net \
        --restart unless-stopped \
        -e NODE_ENV=production \
        -e NEXT_PUBLIC_ADMIN_API_BASE="$NEXT_PUBLIC_ADMIN_API_BASE" \
        -e NEXT_ADMIN_PROXY_TARGET="http://${API_CONTAINER}:8080" \
        -p "${FRONTEND_PORT}:3000"
}

do_stop() {
    check_docker
    stop_container "$ADMIN_CONTAINER"
    stop_container "$API_CONTAINER"
}

do_restart() {
    do_stop
    do_start
}

do_logs() {
    check_docker
    local svc="${2:-}"
    if [[ -n "$svc" ]]; then
        docker logs -f "coin-${svc}"
    else
        # Follow both, prefix lines with service name
        docker logs -f "$API_CONTAINER" 2>&1 | sed "s/^/[api] /" &
        docker logs -f "$ADMIN_CONTAINER" 2>&1 | sed "s/^/[admin] /" &
        wait
    fi
}

do_status() {
    check_docker
    docker ps -f "name=^${API_CONTAINER}$" -f "name=^${ADMIN_CONTAINER}$"
}

case "${1:-start}" in
    start)   do_start   ;;
    stop)    do_stop    ;;
    restart) do_restart ;;
    logs)    do_logs "$@" ;;
    status)  do_status  ;;
    build)   do_build   ;;
    *)
        echo "Usage: $0 {start|stop|restart|logs [api|admin-web]|status|build}" >&2
        exit 1
        ;;
esac

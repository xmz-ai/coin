#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
COMPOSE_FILE="${SCRIPT_DIR}/docker-compose.yml"
ENV_FILE="${SCRIPT_DIR}/.env"
ENV_EXAMPLE_FILE="${SCRIPT_DIR}/.env.example"

usage() {
  cat <<'EOF'
Usage:
  ./deploy/docker/deploy.sh up        # start db/redis, run migrations, start api/admin-web, optional init
  ./deploy/docker/deploy.sh init      # initialize admin + default merchant (if not initialized)
  ./deploy/docker/deploy.sh status    # show compose status + setup status
  ./deploy/docker/deploy.sh logs      # tail all logs
  ./deploy/docker/deploy.sh logs api  # tail one service logs
  ./deploy/docker/deploy.sh down      # stop services (keep volumes/data)
  ./deploy/docker/deploy.sh destroy   # stop and remove volumes/data
EOF
}

ensure_env_file() {
  if [[ ! -f "${ENV_FILE}" ]]; then
    cp "${ENV_EXAMPLE_FILE}" "${ENV_FILE}"
    echo "[deploy] created ${ENV_FILE} from template."
    echo "[deploy] update required secrets before first deploy:"
    echo "         - LOCAL_KMS_KEY_V1"
    echo "         - ADMIN_JWT_SECRET"
  fi
}

load_env() {
  set -a
  # shellcheck disable=SC1090
  source "${ENV_FILE}"
  set +a
}

compose() {
  docker compose --env-file "${ENV_FILE}" -f "${COMPOSE_FILE}" "$@"
}

wait_http() {
  local url="$1"
  local timeout="${2:-120}"
  local elapsed=0
  while (( elapsed < timeout )); do
    if curl -fsS "${url}" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
    elapsed=$((elapsed + 1))
  done
  echo "[deploy] timeout waiting for ${url}" >&2
  return 1
}

json_escape() {
  printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
}

is_initialized_from_status() {
  local raw="$1"
  local compact
  compact="$(printf '%s' "${raw}" | tr -d '\r\n\t ')"
  if [[ "${compact}" == *'"initialized":true'* ]]; then
    return 0
  fi
  return 1
}

init_if_needed() {
  load_env
  local api_port="${API_PORT:-8080}"
  local api_url="http://127.0.0.1:${api_port}"
  local status_json
  status_json="$(curl -fsS "${api_url}/admin/api/v1/setup/status" || true)"
  if [[ -z "${status_json}" ]]; then
    echo "[deploy] failed to query setup status from ${api_url}" >&2
    return 1
  fi
  if is_initialized_from_status "${status_json}"; then
    echo "[deploy] admin setup is already initialized."
    return 0
  fi

  if [[ -z "${ADMIN_SETUP_USERNAME:-}" || -z "${ADMIN_SETUP_PASSWORD:-}" ]]; then
    cat <<EOF
[deploy] setup is not initialized, but ADMIN_SETUP_USERNAME / ADMIN_SETUP_PASSWORD is empty.
[deploy] initialize manually:
curl -sS -X POST ${api_url}/admin/api/v1/setup/initialize \\
  -H 'Content-Type: application/json' \\
  -d '{"admin_username":"<username>","admin_password":"<password>","merchant_name":"Default Merchant"}'
EOF
    return 0
  fi

  local merchant_name="${ADMIN_SETUP_MERCHANT_NAME:-Default Merchant}"
  local payload
  payload="$(printf '{"admin_username":"%s","admin_password":"%s","merchant_name":"%s"}' \
    "$(json_escape "${ADMIN_SETUP_USERNAME}")" \
    "$(json_escape "${ADMIN_SETUP_PASSWORD}")" \
    "$(json_escape "${merchant_name}")")"

  local init_resp
  init_resp="$(curl -sS -X POST "${api_url}/admin/api/v1/setup/initialize" \
    -H 'Content-Type: application/json' \
    -d "${payload}")"
  printf '%s\n' "${init_resp}" > "${SCRIPT_DIR}/admin_setup_result.json"

  local compact
  compact="$(printf '%s' "${init_resp}" | tr -d '\r\n\t ')"
  if [[ "${compact}" == *'"code":"SUCCESS"'* ]]; then
    echo "[deploy] setup initialized successfully."
    echo "[deploy] initialization response saved to: ${SCRIPT_DIR}/admin_setup_result.json"
    return 0
  fi

  echo "[deploy] setup initialize request failed. raw response:"
  echo "${init_resp}"
  return 1
}

deploy_up() {
  load_env

  echo "[deploy] starting db and redis..."
  compose up -d db redis

  echo "[deploy] running migrations..."
  compose run --rm migrate

  echo "[deploy] starting api and admin-web..."
  compose up -d api admin-web

  local api_port="${API_PORT:-8080}"
  local admin_web_port="${ADMIN_WEB_PORT:-3000}"
  local api_url="http://127.0.0.1:${api_port}"
  local admin_url="http://127.0.0.1:${admin_web_port}"

  echo "[deploy] waiting for api health..."
  wait_http "${api_url}/healthz" 180
  echo "[deploy] waiting for admin web..."
  wait_http "${admin_url}/login" 180

  echo "[deploy] running optional setup initialization..."
  init_if_needed

  echo "[deploy] done."
  echo "[deploy] api:       ${api_url}"
  echo "[deploy] admin web: ${admin_url}"
}

show_status() {
  load_env
  compose ps
  local api_port="${API_PORT:-8080}"
  local api_url="http://127.0.0.1:${api_port}"
  echo "[deploy] setup status:"
  curl -sS "${api_url}/admin/api/v1/setup/status" || true
  echo
}

main() {
  ensure_env_file
  local action="${1:-up}"
  case "${action}" in
    up)
      deploy_up
      ;;
    init)
      init_if_needed
      ;;
    status)
      show_status
      ;;
    logs)
      if [[ "${2:-}" == "" ]]; then
        compose logs -f
      else
        compose logs -f "$2"
      fi
      ;;
    down)
      compose down
      ;;
    destroy)
      compose down -v
      ;;
    *)
      usage
      exit 1
      ;;
  esac
}

main "$@"

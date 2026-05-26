#!/usr/bin/env bash
set -euo pipefail

failures=0
DOCKER_INFO_TIMEOUT_SECONDS="${DOCKER_INFO_TIMEOUT_SECONDS:-5}"

pass() {
  printf 'OK: %s\n' "$1"
}

fail() {
  printf 'FAIL: %s\n' "$1" >&2
  failures=$((failures + 1))
}

run_with_timeout() {
  local timeout_seconds="$1"
  shift
  local tmp_status
  tmp_status="$(mktemp)"
  "$@" &
  local pid=$!
  (
    sleep "$timeout_seconds"
    if kill -0 "$pid" >/dev/null 2>&1; then
      kill "$pid" >/dev/null 2>&1 || true
      sleep 1
      kill -9 "$pid" >/dev/null 2>&1 || true
    fi
  ) &
  local watcher=$!
  local status=0
  if wait "$pid"; then
    status=0
  else
    status=$?
  fi
  kill "$watcher" >/dev/null 2>&1 || true
  wait "$watcher" >/dev/null 2>&1 || true
  printf '%s' "$status" > "$tmp_status"
  status="$(cat "$tmp_status")"
  rm -f "$tmp_status"
  return "$status"
}

docker_endpoint() {
  docker context inspect --format '{{ (index .Endpoints "docker").Host }}' 2>/dev/null || true
}

if command -v docker >/dev/null 2>&1; then
  endpoint="$(docker_endpoint)"
  context_name="$(docker context show 2>/dev/null || true)"
  if [ -n "$context_name" ]; then
    pass "Docker context is ${context_name}${endpoint:+ ($endpoint)}"
  fi
  if [[ "$endpoint" == unix://* ]]; then
    socket_path="${endpoint#unix://}"
    if [ ! -S "$socket_path" ]; then
      fail "Docker context socket is missing at $socket_path"
    fi
  fi
  if run_with_timeout "$DOCKER_INFO_TIMEOUT_SECONDS" docker info >/dev/null 2>&1; then
    pass "Docker daemon is reachable"
  else
    fail "Docker is installed but the daemon is not reachable within ${DOCKER_INFO_TIMEOUT_SECONDS}s"
  fi
else
  fail "Docker CLI is not installed"
fi

if [ -n "${VULPINE_API_KEY:-}" ]; then
  pass "VULPINE_API_KEY is set"
else
  fail "VULPINE_API_KEY is not set"
fi

browser_artifact="dist/camoufox-linux/camoufox"
if [ -x "$browser_artifact" ]; then
  if command -v file >/dev/null 2>&1; then
    artifact_type="$(file -b "$browser_artifact" || true)"
    if printf '%s' "$artifact_type" | grep -qi 'ELF'; then
      pass "Linux browser artifact is executable ELF at $browser_artifact"
    else
      fail "$browser_artifact is executable but does not look like a Linux ELF ($artifact_type)"
    fi
  else
    pass "Linux browser artifact exists at $browser_artifact"
  fi
elif [ -e "$browser_artifact" ]; then
  fail "$browser_artifact exists but is not executable"
else
  fail "Linux browser artifact missing at $browser_artifact"
fi

if [ -f "Dockerfile.vulpinebox" ]; then
  pass "Dockerfile.vulpinebox exists"
else
  fail "Dockerfile.vulpinebox is missing"
fi

if [ -f "docker-compose.yml" ]; then
  pass "docker-compose.yml exists"
  if VULPINE_API_KEY="${VULPINE_API_KEY:-preflight-dummy}" docker compose config >/dev/null 2>&1; then
    pass "docker compose config parses"
  else
    fail "docker compose config failed"
  fi
else
  fail "docker-compose.yml is missing"
fi

if [ "$failures" -ne 0 ]; then
  cat >&2 <<'EOF'

Vulpine-Box preflight failed.

Expected setup:
  export VULPINE_API_KEY=$(openssl rand -hex 32)
  # place the Linux Camoufox artifact at:
  # dist/camoufox-linux/camoufox
  # start Docker Desktop or your Docker daemon

Then rerun:
  ./scripts/check-vulpinebox.sh
  docker compose up -d
EOF
  exit 1
fi

printf 'Vulpine-Box preflight passed. Run: docker compose up -d\n'

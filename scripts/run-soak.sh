#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && git rev-parse --show-toplevel)"
ITERATIONS="${1:-${VULPINEOS_SOAK_ITERATIONS:-3}}"
ARTIFACT_DIR="${VULPINEOS_SOAK_ARTIFACT_DIR:-${ROOT_DIR}/.artifacts/soak}"
TIMESTAMP="$(date -u +"%Y%m%dT%H%M%SZ")"
LOG_FILE="${ARTIFACT_DIR}/soak-${TIMESTAMP}.log"
JSON_FILE="${ARTIFACT_DIR}/soak-${TIMESTAMP}.json"

mkdir -p "${ARTIFACT_DIR}"

STARTED_AT="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
START_EPOCH="$(python3 - <<'PY'
import time
print(f"{time.time():.6f}")
PY
)"

echo "Running scoped-session soak harness"
echo "  iterations: ${ITERATIONS}"
echo "  log:        ${LOG_FILE}"
echo "  artifact:   ${JSON_FILE}"

set +e
(
  cd "${ROOT_DIR}"
  VULPINEOS_RUN_SOAK=1 \
  VULPINEOS_RUN_LIVE=1 \
  VULPINEOS_SOAK_ITERATIONS="${ITERATIONS}" \
  go test ./internal/mcp -run TestLiveScopedSessionSoak -count=1 -v
) 2>&1 | tee "${LOG_FILE}"
TEST_EXIT="${PIPESTATUS[0]}"
set -e

FINISHED_AT="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"

set +e
python3 - "${LOG_FILE}" "${JSON_FILE}" "${ITERATIONS}" "${STARTED_AT}" "${FINISHED_AT}" "${START_EPOCH}" "${TEST_EXIT}" <<'PY'
import json
import pathlib
import re
import sys
import time

log_path = pathlib.Path(sys.argv[1])
json_path = pathlib.Path(sys.argv[2])
iterations = int(sys.argv[3])
started_at = sys.argv[4]
finished_at = sys.argv[5]
start_epoch = float(sys.argv[6])
exit_code = int(sys.argv[7])

pattern = re.compile(r"SOAK_RESULT iteration=(?P<iteration>\d+) duration_ms=(?P<duration_ms>\d+) cleanup_session=(?P<cleanup_session>\S+) status=(?P<status>\S+)")
results = []
for line in log_path.read_text().splitlines():
    match = pattern.search(line)
    if not match:
        continue
    payload = match.groupdict()
    results.append({
        "iteration": int(payload["iteration"]),
        "duration_ms": int(payload["duration_ms"]),
        "cleanup_session": payload["cleanup_session"],
        "status": payload["status"],
    })

artifact = {
    "version": 1,
    "started_at": started_at,
    "finished_at": finished_at,
    "duration_seconds": round(time.time() - start_epoch, 3),
    "iterations_requested": iterations,
    "iterations_completed": len(results),
    "status": "passed" if exit_code == 0 and len(results) == iterations else "failed",
    "exit_code": exit_code,
    "log_file": str(log_path),
    "results": results,
}

json_path.write_text(json.dumps(artifact, indent=2) + "\n")
if artifact["status"] != "passed":
    sys.exit(1)
PY
ARTIFACT_EXIT="$?"
set -e

echo "Wrote soak artifact to ${JSON_FILE}"
if [[ "${ARTIFACT_EXIT}" -ne 0 && "${TEST_EXIT}" -eq 0 ]]; then
  echo "Soak artifact validation failed: expected ${ITERATIONS} result lines"
  exit "${ARTIFACT_EXIT}"
fi
exit "${TEST_EXIT}"

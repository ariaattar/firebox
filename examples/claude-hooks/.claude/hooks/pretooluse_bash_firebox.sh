#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "${SCRIPT_DIR}/../.." && pwd)"
SDK_DIR="${REPO_ROOT}/sdk/firebox-sdk"
SDK_CLI="${SDK_DIR}/dist/cli.js"
FIREBOX_BIN_DEFAULT="${REPO_ROOT}/firebox"

HOOK_INPUT="$(cat)"
if [[ -z "${HOOK_INPUT}" ]]; then
  exit 0
fi

if [[ ! -f "${SDK_CLI}" ]]; then
  if ! command -v npm >/dev/null 2>&1; then
    exit 0
  fi

  (
    cd "${SDK_DIR}"
    npm install --silent >/dev/null 2>&1
    npm run build --silent >/dev/null 2>&1
  ) || exit 0
fi

if [[ ! -f "${SDK_CLI}" ]]; then
  exit 0
fi

FIREBOX_BIN="${FIREBOX_BIN:-${FIREBOX_BIN_DEFAULT}}"

printf '%s' "${HOOK_INPUT}" | node "${SDK_CLI}" \
  --firebox-bin "${FIREBOX_BIN}" \
  claude-hook pretooluse-bash \
  --permission ask || exit 0

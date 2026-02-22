#!/usr/bin/env bash
set -euo pipefail

STATE_FILE_DEFAULT="${HOME}/.config/firebox/firebox-sdk.json"
STATE_FILE="${FIREBOX_STATE_FILE:-${STATE_FILE_DEFAULT}}"

if [[ ! -f "${STATE_FILE}" ]]; then
  exit 0
fi

MODE="$(grep -E '"mode"[[:space:]]*:[[:space:]]*"' "${STATE_FILE}" | head -n1 | sed -E 's/.*"mode"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/' || true)"
if [[ "${MODE}" != "on-no-cow" && "${MODE}" != "on-cow" ]]; then
  exit 0
fi

MESSAGE="Firebox is active for this project (mode: ${MODE}). Bash commands will be wrapped through Firebox."
printf '{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"%s"},"systemMessage":"%s"}\n' "${MESSAGE}" "${MESSAGE}"

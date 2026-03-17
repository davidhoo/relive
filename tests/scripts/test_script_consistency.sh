#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

fail() {
  echo "FAIL: $1" >&2
  exit 1
}

# 1) Entry point should NOT call init-cities (embedded data now)
if grep -q "/app/init-cities.sh" "$ROOT/backend/scripts/docker-entrypoint.sh"; then
  fail "docker-entrypoint.sh still calls /app/init-cities.sh (should use embedded data)"
fi

# 2) deploy script should not mention QWEN/OPENAI keys
if rg -n "QWEN_API_KEY|OPENAI_API_KEY" "$ROOT/deploy.sh" >/dev/null; then
  fail "deploy.sh mentions QWEN/OPENAI API keys"
fi

# 3) core scripts should use set -e
for script in dev.sh deploy.sh test-local.sh; do
  if ! rg -q "^set -e" "$ROOT/$script"; then
    fail "$script does not enable 'set -e'"
  fi
done

echo "OK: script consistency checks passed"

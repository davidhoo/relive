#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

fail() {
  echo "FAIL: $1" >&2
  exit 1
}

# 1) Entry point should delegate to init-cities
grep -q "/app/init-cities.sh" "$ROOT/backend/scripts/docker-entrypoint.sh" \
  || fail "docker-entrypoint.sh does not call /app/init-cities.sh"

# 2) init-cities should gate on AUTO_IMPORT_CITIES
grep -q "AUTO_IMPORT_CITIES" "$ROOT/backend/scripts/init-cities.sh" \
  || fail "init-cities.sh does not check AUTO_IMPORT_CITIES"

# 3) init-cities should check cities500.txt existence
grep -q "cities500.txt" "$ROOT/backend/scripts/init-cities.sh" \
  || fail "init-cities.sh does not check cities500.txt"

# 4) init-cities should use deleted_at filter for count
grep -q "deleted_at IS NULL" "$ROOT/backend/scripts/init-cities.sh" \
  || fail "init-cities.sh does not use deleted_at IS NULL in count query"

# 5) deploy/install env templates should not mention QWEN/OPENAI keys
if rg -n "QWEN_API_KEY|OPENAI_API_KEY" "$ROOT/deploy.sh" "$ROOT/install.sh" >/dev/null; then
  fail "deploy.sh or install.sh mentions QWEN/OPENAI API keys"
fi

# 6) import-geonames should accept config path
if ! rg -q -- "--config" "$ROOT/backend/import-geonames.sh"; then
  fail "import-geonames.sh does not support --config"
fi

echo "OK: script consistency checks passed"

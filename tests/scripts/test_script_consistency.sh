#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

fail() {
  echo "FAIL: $1" >&2
  exit 1
}

assert_contains() {
  local haystack="$1"
  local needle="$2"
  local description="$3"
  if ! grep -Fq -- "$needle" <<<"$haystack"; then
    fail "$description"
  fi
}

assert_not_contains() {
  local haystack="$1"
  local needle="$2"
  local description="$3"
  if grep -Fq -- "$needle" <<<"$haystack"; then
    fail "$description"
  fi
}

MAKE_HELP="$(make -C "$ROOT" help)"
MAKE_DEV="$(make -C "$ROOT" -n dev)"
MAKE_DEPLOY="$(make -C "$ROOT" -n deploy)"
MAKE_DEPLOY_IMAGE="$(make -C "$ROOT" -n deploy-image)"
TMPDIR_MAKE="$(mktemp -d)"
trap 'rm -rf "$TMPDIR_MAKE"' EXIT

cp "$ROOT/Makefile" "$TMPDIR_MAKE/Makefile"
cp "$ROOT/docker-compose.prod.yml.example" "$TMPDIR_MAKE/docker-compose.prod.yml"

IMAGE_ONLY_LOGS="$(make -C "$TMPDIR_MAKE" -n logs)"
IMAGE_ONLY_STOP="$(make -C "$TMPDIR_MAKE" -n stop)"
IMAGE_ONLY_RESTART="$(make -C "$TMPDIR_MAKE" -n restart)"

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

# 4) make help should advertise only the approved public interface
for target in \
  "make dev" \
  "make build" \
  "make deploy" \
  "make deploy-image" \
  "make logs" \
  "make stop" \
  "make restart" \
  "make test" \
  "make clean" \
  "make build-analyzer"
do
  assert_contains "$MAKE_HELP" "$target" "make help does not include approved target: $target"
done

for target in \
  "make dev-backend" \
  "make dev-frontend" \
  "make analyzer" \
  "make prod" \
  "make deps"
do
  assert_not_contains "$MAKE_HELP" "$target" "make help still advertises deprecated target: $target"
done

# 5) the public entrypoints should dispatch to the approved scripts
assert_contains "$MAKE_DEV" "./dev.sh" "make -n dev does not invoke ./dev.sh"
assert_contains "$MAKE_DEPLOY" "./deploy.sh" "make -n deploy does not invoke ./deploy.sh"
assert_contains "$MAKE_DEPLOY_IMAGE" "./deploy-image.sh" "make -n deploy-image does not invoke ./deploy-image.sh"

# 6) dev.sh should be a deterministic full-stack local entrypoint
DEV_SCRIPT="$(cat "$ROOT/dev.sh")"
assert_not_contains "$DEV_SCRIPT" "请选择启动模式" "dev.sh still contains an interactive menu prompt"
assert_not_contains "$DEV_SCRIPT" "read -p" "dev.sh still uses read -p"
assert_contains "$DEV_SCRIPT" "go run cmd/relive/main.go --config config.dev.yaml" "dev.sh no longer starts the backend locally"
assert_contains "$DEV_SCRIPT" "npm run dev" "dev.sh no longer starts the frontend locally"
assert_contains "$DEV_SCRIPT" "trap" "dev.sh no longer installs cleanup handling for child processes"

# 7) deploy.sh should stay source-only and let Docker build the app image
DEPLOY_SCRIPT="$(cat "$ROOT/deploy.sh")"
assert_not_contains "$DEPLOY_SCRIPT" "npm install" "deploy.sh still installs frontend dependencies on the host"
assert_not_contains "$DEPLOY_SCRIPT" "npm run build" "deploy.sh still builds the frontend on the host"
assert_contains "$DEPLOY_SCRIPT" "docker compose build" "deploy.sh no longer performs compose-based image builds"
assert_contains "$DEPLOY_SCRIPT" "docker compose up -d" "deploy.sh no longer performs compose-based startup"

# 8) deploy-image.sh should pull and start the published image stack
DEPLOY_IMAGE_SCRIPT="$(cat "$ROOT/deploy-image.sh")"
assert_contains "$DEPLOY_IMAGE_SCRIPT" "docker compose -f docker-compose.prod.yml pull" "deploy-image.sh does not pull published images"
assert_contains "$DEPLOY_IMAGE_SCRIPT" "docker compose -f docker-compose.prod.yml up -d" "deploy-image.sh does not start the published image stack"

# 9) user-facing docs should match the approved Make interface
README_TEXT="$(cat "$ROOT/README.md")"
QUICKSTART_TEXT="$(cat "$ROOT/QUICKSTART.md")"
QUICK_REFERENCE_TEXT="$(cat "$ROOT/docs/QUICK_REFERENCE.md")"
DOCS_TEXT="${README_TEXT}"$'\n'"${QUICKSTART_TEXT}"$'\n'"${QUICK_REFERENCE_TEXT}"

assert_contains "$README_TEXT" "make deploy-image" "README no longer recommends image deployment"
assert_contains "$README_TEXT" "已发布镜像" "README does not describe image deployment as the default user path"
assert_contains "$README_TEXT" "make deploy" "README no longer documents source deployment"
assert_contains "$README_TEXT" "源码" "README does not describe make deploy as source deployment"

assert_contains "$QUICKSTART_TEXT" "make deploy-image" "QUICKSTART does not include the image deployment path"
assert_contains "$QUICKSTART_TEXT" "已发布镜像" "QUICKSTART does not position image deployment first"
assert_contains "$QUICKSTART_TEXT" "make deploy" "QUICKSTART no longer documents source deployment"
assert_contains "$QUICKSTART_TEXT" "源码" "QUICKSTART does not describe make deploy as source deployment"

assert_contains "$QUICK_REFERENCE_TEXT" "make deploy-image" "docs/QUICK_REFERENCE.md does not list make deploy-image"

for target in \
  "make analyzer" \
  "make prod" \
  "make dev-backend" \
  "make dev-frontend" \
  "make dev-ml"
do
  assert_not_contains "$DOCS_TEXT" "$target" "Docs still advertise deprecated target: $target"
done

# 10) operational targets should work for image-only installs
assert_contains "$IMAGE_ONLY_LOGS" "-f docker-compose.prod.yml logs -f" "make -n logs does not target docker-compose.prod.yml for image-only installs"
assert_contains "$IMAGE_ONLY_STOP" "-f docker-compose.prod.yml down" "make -n stop does not target docker-compose.prod.yml for image-only installs"
assert_contains "$IMAGE_ONLY_RESTART" "-f docker-compose.prod.yml restart" "make -n restart does not target docker-compose.prod.yml for image-only installs"
assert_not_contains "$IMAGE_ONLY_LOGS" "docker-compose.yml 不存在" "make -n logs still requires docker-compose.yml for image-only installs"
assert_not_contains "$IMAGE_ONLY_STOP" "docker-compose.yml 不存在" "make -n stop still requires docker-compose.yml for image-only installs"
assert_not_contains "$IMAGE_ONLY_RESTART" "docker-compose.yml 不存在" "make -n restart still requires docker-compose.yml for image-only installs"

echo "OK: script consistency checks passed"

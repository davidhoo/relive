# Script Reliability and Consistency Design (Plan A)

Date: 2026-03-10

## Context

Relive currently ships multiple shell scripts for development, deployment, testing, and runtime entrypoints. These scripts overlap in responsibilities and contain inconsistent defaults, which reduces reliability and makes behavior harder to predict. The goal is to keep the scripts but make their behavior consistent and centralized where possible.

## Goals

- Improve reliability by reducing duplicated logic and contradictory defaults.
- Make script behavior consistent across local dev, local deploy, and one-click install.
- Preserve current interactive UX and keep the scripts familiar to developers.
- Keep self-deploy flows friendly and predictable.

## Non-goals

- Removing interactivity or making scripts CI-only.
- Rewriting scripts into a different language.
- Changing core product behavior beyond script consistency.

## Inventory (Current Scripts)

- `dev.sh`: interactive dev launcher (backend/frontend).
- `deploy.sh`: local build + compose deployment.
- `install.sh`: one-click install from DockerHub.
- `test-local.sh`: quick local container test.
- `build-multiarch.sh`: multi-arch build and push.
- `backend/scripts/docker-entrypoint.sh`: container entrypoint.
- `backend/scripts/init-cities.sh`: city data import helper.
- `backend/import-geonames.sh`: developer-only offline city data importer.
- `Makefile` / `backend/Makefile`: command entry points for developers.

## Proposed Changes (Plan A)

### 1) Unify `.env` generation and defaults

Align `.env` generation in `deploy.sh` and `install.sh` with `.env.example`:
- Same keys and ordering.
- Same comments.
- No AI provider keys (those are configured via the admin UI).

### 2) Single source of truth for city data auto-import

Make `init-cities.sh` the single implementation of auto-import logic:
- `docker-entrypoint.sh` delegates to `/app/init-cities.sh`.
- `init-cities.sh` handles:
  - `AUTO_IMPORT_CITIES` gating.
  - file existence check for `cities500.txt`.
  - consistent DB check (use `cities` count with `deleted_at IS NULL`).
  - clear and consistent output messages.

This removes duplicated logic and mismatched thresholds in the entrypoint.

### 3) Config path consistency

Define consistent defaults by scenario:
- Dev scripts use `config.dev.yaml`.
- Deploy/test scripts use `config.prod.yaml`.
- `import-geonames.sh` accepts an optional config path flag and defaults to `config.dev.yaml`, documenting when to use `config.prod.yaml`.

### 4) Output and error handling consistency

Standardize:
- `set -e` and explicit non-zero exits on failure.
- Error messages with clear next steps (missing config, missing dependencies).
- Uniform color/labeling patterns for success/warn/error.

## Data Flow (After Changes)

- `dev.sh`:
  - Ensures `backend/config.dev.yaml` exists.
  - Starts backend and frontend.
- `deploy.sh`:
  - Ensures `.env` and `backend/config.prod.yaml` exist and match template.
  - Builds locally and deploys via compose.
- `install.sh`:
  - Downloads prod compose + config.
  - Generates `.env` consistent with `.env.example`.
- `docker-entrypoint.sh`:
  - Validates config selection.
  - Calls `/app/init-cities.sh` for auto-import.
  - Starts the main app.

## Error Handling

All scripts:
- Exit with non-zero code on missing required files or dependencies.
- Print a concrete remediation command (e.g., copy from `.example`).
- Avoid silent fallbacks that hide errors.

## Testing / Verification

Minimal checks:
- `bash -n` on shell scripts after edits.
- Optional `shellcheck` if available.
- Manual sanity runs:
  - `./dev.sh` (backend only).
  - `./deploy.sh --quick` (no data download).
  - `./test-local.sh` (container boot + health check).

## Risks

- Consolidating auto-import logic changes timing and may affect edge cases; mitigation is to keep behavior equivalent and preserve `AUTO_IMPORT_CITIES` gating.
- Standardizing templates may remove undocumented environment keys; mitigation is to keep `.env.example` as the source of truth and document changes.


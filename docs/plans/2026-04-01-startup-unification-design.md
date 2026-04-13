# Startup Unification Design

**Date:** 2026-04-01

> **Status:** Candidate
> **Note:** The corresponding code changes were rolled back from `main`. Keep this document as a future option, not as a description of the current runtime.

**Problem**

The repository currently has three incompatible startup paths:

- `make dev` runs local processes and may omit the ML service.
- `make deploy` builds and runs Docker services.
- `docker compose up -d` expects a full Docker stack.

This split causes repeated drift in service coverage, especially for the ML sidecar. In practice, face detection fails because the backend expects `relive-ml`, while local development paths may not start it or may start it with an incompatible local Python runtime.

**Goals**

- `make dev` must start the full required backend stack for development.
- `make deploy` must start the full production-like Docker stack.
- `docker compose up -d` must also start the full stack without additional manual service selection.
- Development must preserve fast frontend HMR and browser debugging.

**Approved Approach**

Use Docker as the single runtime for `backend + relive-ml`, while keeping the frontend local only in development.

- Development:
  - `backend + relive-ml` run in Docker Compose.
  - `frontend` runs locally with Vite for hot reload.
- Deployment:
  - `relive + relive-ml` both run in Docker Compose.
  - Static frontend is built into the deployed `relive` image as today.

**Why This Approach**

- It removes local Python drift from the ML service path.
- It keeps one authoritative definition of the backend service graph.
- It preserves the best frontend developer experience.
- It makes `make deploy` and `docker compose up -d` naturally equivalent in service coverage.

**Runtime Model**

- Base Compose file defines the full application stack:
  - `relive`
  - `relive-ml`
- Development Compose override changes `relive` from production image mode to Go source-mounted dev mode and adds any dev-only mounts or env vars.
- Frontend development remains outside Docker and is launched by `make dev`.

**Required Script/Config Changes**

- `Makefile`
  - `make dev` becomes a non-interactive entry point that starts Dockerized backend services and local frontend.
  - `make deploy` remains the deployment entry point but relies on the same Compose service graph.
- `dev.sh`
  - Replace menu-driven partial startup with an opinionated full dev startup.
  - Start `relive` and `relive-ml` via Compose.
  - Start local Vite separately.
- `docker-compose.yml.example`
  - Continue to define the full deployable stack.
- `docker-compose.dev.yml`:
  - Add development overrides for backend code mounts, command, config, and any dev-only ports or environment.
- `deploy.sh`
  - Keep full Docker deployment behavior aligned with the same service set.

**ML Service Constraint**

The ML service must not depend on the host Python environment for the normal dev path. Docker becomes the supported runtime for development and deployment. The Docker image itself must build successfully on supported developer machines.

**Verification Strategy**

Use a script regression test for startup consistency plus command-level verification:

- `tests/scripts/test_script_consistency.sh`
- `docker compose config`
- `docker compose -f docker-compose.yml -f docker-compose.dev.yml config`
- targeted `make -n` or script-level smoke checks

**Non-Goals**

- No attempt to preserve the old interactive startup menu.
- No attempt to support ML development directly on arbitrary host Python versions.
- No change to analyzer architecture in this task.

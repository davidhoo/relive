# Startup Unification Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.
>
> **Status:** Candidate
> **Note:** The corresponding code changes were rolled back from `main`. Keep this plan as a future option, not as the current implementation roadmap.

**Goal:** Make `make dev`, `make deploy`, and `docker compose up -d` all bring up the complete required service stack while preserving local frontend HMR in development.

**Architecture:** Use Docker Compose as the single source of truth for backend service topology. Keep `relive` and `relive-ml` in Compose for both dev and deploy, then layer a dev override for source-mounted Go development while running Vite locally outside Docker.

**Tech Stack:** Make, Bash, Docker Compose, Go, Vue/Vite, FastAPI, InsightFace sidecar

---

### Task 1: Lock the expected startup contract in a regression test

**Files:**
- Modify: `tests/scripts/test_script_consistency.sh`

**Step 1: Write the failing test**

Add assertions that:

- `Makefile` `dev` target invokes `./dev.sh`.
- `Makefile` `deploy` target invokes `./deploy.sh`.
- `docker-compose.yml.example` defines both `relive` and `relive-ml`.
- `dev.sh` starts Compose-based backend services instead of local `go run` / local Python ML startup.

**Step 2: Run test to verify it fails**

Run: `bash tests/scripts/test_script_consistency.sh`
Expected: FAIL because `dev.sh` still contains local `go run` and local Python ML startup paths.

**Step 3: Write minimal implementation**

Update the test with specific `rg` / `grep` assertions matching the desired behavior.

**Step 4: Run test to verify it passes**

Run: `bash tests/scripts/test_script_consistency.sh`
Expected: PASS after script refactor.

### Task 2: Add a Compose development override

**Files:**
- Create: `docker-compose.dev.yml`

**Step 1: Write the failing test**

Extend the script test to assert that `docker-compose.dev.yml` exists and includes a `relive` service override.

**Step 2: Run test to verify it fails**

Run: `bash tests/scripts/test_script_consistency.sh`
Expected: FAIL because the override file does not exist yet.

**Step 3: Write minimal implementation**

Create `docker-compose.dev.yml` to:

- override `relive` build/runtime for Go dev
- mount backend source and dev config
- keep `relive-ml` in the same network/runtime graph

**Step 4: Run test to verify it passes**

Run: `bash tests/scripts/test_script_consistency.sh`
Expected: PASS for the new file presence/shape checks.

### Task 3: Refactor dev entrypoints to use Compose for backend services

**Files:**
- Modify: `dev.sh`
- Modify: `Makefile`

**Step 1: Write the failing test**

Extend the script test to assert that:

- `dev.sh` uses `docker compose -f docker-compose.yml -f docker-compose.dev.yml up`
- `dev.sh` no longer contains the interactive menu
- `dev.sh` no longer starts local ML with `python3 -m venv` or `uvicorn`
- `dev.sh` no longer starts backend with local `go run`

**Step 2: Run test to verify it fails**

Run: `bash tests/scripts/test_script_consistency.sh`
Expected: FAIL on one or more legacy behaviors.

**Step 3: Write minimal implementation**

Refactor:

- `dev.sh` into a simple dev bootstrapper:
  - ensure config files exist
  - start Docker Compose backend services
  - install frontend deps if needed
  - run local Vite
- `Makefile` help text and targets to match the new behavior

**Step 4: Run test to verify it passes**

Run: `bash tests/scripts/test_script_consistency.sh`
Expected: PASS

### Task 4: Make ML Docker build viable for supported paths

**Files:**
- Modify: `ml-service/Dockerfile`

**Step 1: Write the failing test**

Add a targeted script assertion that `ml-service/Dockerfile` installs compiler support required for `insightface` builds.

**Step 2: Run test to verify it fails**

Run: `bash tests/scripts/test_script_consistency.sh`
Expected: FAIL because `g++` / build tooling is missing.

**Step 3: Write minimal implementation**

Update the Dockerfile to install the minimal compiler/build packages needed for `pip install -r requirements.txt`.

**Step 4: Run test to verify it passes**

Run: `bash tests/scripts/test_script_consistency.sh`
Expected: PASS

### Task 5: Align deploy/docs with the unified model

**Files:**
- Modify: `deploy.sh`
- Modify: `README.md`
- Modify: `QUICKSTART.md`
- Optionally modify: `docker-compose.prod.yml.example`

**Step 1: Write the failing test**

Add assertions that user-facing docs and scripts describe:

- `make dev` as Dockerized backend + local frontend
- `make deploy` as full Docker deployment
- `docker compose up -d` as a complete stack startup

**Step 2: Run test to verify it fails**

Run: `bash tests/scripts/test_script_consistency.sh`
Expected: FAIL because docs still describe the old startup behavior.

**Step 3: Write minimal implementation**

Update docs and deploy messaging to match the new model.

**Step 4: Run test to verify it passes**

Run: `bash tests/scripts/test_script_consistency.sh`
Expected: PASS

### Task 6: Verify the stack definitions end to end

**Files:**
- No file changes required

**Step 1: Run compose validation**

Run: `docker compose -f docker-compose.yml -f docker-compose.dev.yml config >/tmp/relive-dev-compose.out`
Expected: success, with `relive` and `relive-ml` both present

**Step 2: Run deploy compose validation**

Run: `docker compose config >/tmp/relive-compose.out`
Expected: success, with `relive` and `relive-ml` both present

**Step 3: Run script regression test**

Run: `bash tests/scripts/test_script_consistency.sh`
Expected: PASS

**Step 4: Smoke-check make targets**

Run: `make -n dev`
Expected: shows `./dev.sh`

Run: `make -n deploy`
Expected: shows `./deploy.sh`

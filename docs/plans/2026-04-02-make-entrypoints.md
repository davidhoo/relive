# Make Entrypoints Cleanup Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.
>
> **Status:** Completed
> **Note:** Implemented on `main`; retained for historical traceability.

**Goal:** Reduce the root Make interface to a small approved command set, make `make dev` non-interactive, split source deployment from image deployment, and align docs/tests with that contract.

**Architecture:** Keep the root `Makefile` as the project-level entrypoint. Use `dev.sh` for local development startup, `deploy.sh` for source-based deployment, and a new image deployment entrypoint for published-image installs. Lock the public interface with a script regression test and update primary docs to recommend image deployment for users.

**Tech Stack:** GNU Make, Bash, Docker Compose, Go, Vue/Vite, Markdown docs

---

### Task 1: Lock the approved root Make contract in a regression test

**Files:**
- Modify: `tests/scripts/test_script_consistency.sh`

**Step 1: Write the failing test**

Add assertions that:

- `make help` includes:
  - `make dev`
  - `make build`
  - `make deploy`
  - `make deploy-image`
  - `make logs`
  - `make stop`
  - `make restart`
  - `make test`
  - `make clean`
  - `make build-analyzer`
- `make help` does not advertise:
  - `make dev-backend`
  - `make dev-frontend`
  - `make analyzer`
  - `make prod`
  - `make deps`
- `make -n dev` prints `./dev.sh`
- `make -n deploy` prints `./deploy.sh`
- `make -n deploy-image` prints the new image deployment entrypoint

**Step 2: Run test to verify it fails**

Run: `bash tests/scripts/test_script_consistency.sh`
Expected: FAIL because the old help output and targets are still present.

**Step 3: Write minimal implementation**

Update the test to use `make help`, `make -n ...`, `rg`, and `grep` checks matching the approved contract.

**Step 4: Run test to verify it passes**

Run: `bash tests/scripts/test_script_consistency.sh`
Expected: PASS after interface changes are implemented.

### Task 2: Simplify the root Makefile to the approved public interface

**Files:**
- Modify: `Makefile`

**Step 1: Write the failing test**

Rely on the updated script regression test from Task 1.

**Step 2: Run test to verify it fails**

Run: `bash tests/scripts/test_script_consistency.sh`
Expected: FAIL on outdated public help text and missing `deploy-image`.

**Step 3: Write minimal implementation**

Update `Makefile` to:

- keep public targets:
  - `dev`
  - `build`
  - `deploy`
  - `deploy-image`
  - `logs`
  - `stop`
  - `restart`
  - `test`
  - `clean`
  - `build-analyzer`
- remove `analyzer` from the public interface
- remove `dev-backend`, `dev-frontend`, and `deps` from help output
- replace `prod` in help output with `deploy-image`
- keep helper targets private
- optionally keep `prod` as an undocumented compatibility alias to `deploy-image`

**Step 4: Run test to verify it passes**

Run: `bash tests/scripts/test_script_consistency.sh`
Expected: PASS for help and `make -n` shape checks after adding `deploy-image`.

### Task 3: Refactor `make dev` into a deterministic local development entrypoint

**Files:**
- Modify: `dev.sh`
- Modify: `Makefile`

**Step 1: Write the failing test**

Extend the regression script to assert that:

- `dev.sh` no longer contains the interactive menu prompt
- `dev.sh` no longer uses `read -p`
- `dev.sh` directly starts the full local development flow

**Step 2: Run test to verify it fails**

Run: `bash tests/scripts/test_script_consistency.sh`
Expected: FAIL because `dev.sh` is still menu-driven.

**Step 3: Write minimal implementation**

Refactor `dev.sh` to:

- ensure `backend/config.dev.yaml` exists
- ensure local runtime directories exist
- start backend locally with `go run ... --config config.dev.yaml`
- start frontend locally with `npm run dev`
- use a single fixed flow with `trap` cleanup

Keep the behavior equivalent to the current "start both" path, but remove the menu and branching.

**Step 4: Run test to verify it passes**

Run: `bash tests/scripts/test_script_consistency.sh`
Expected: PASS for the non-interactive dev contract.

### Task 4: Narrow `deploy.sh` to source deployment only

**Files:**
- Modify: `deploy.sh`
- Modify: `Makefile`
- Reference: `Dockerfile`

**Step 1: Write the failing test**

Extend the regression script to assert that:

- `deploy.sh` remains the entrypoint for `make deploy`
- `deploy.sh` does not run host-side `npm run build`
- `deploy.sh` does not require host-side `npm install` for deployment
- `deploy.sh` still performs compose-based source deployment

**Step 2: Run test to verify it fails**

Run: `bash tests/scripts/test_script_consistency.sh`
Expected: FAIL because `deploy.sh` still performs host-side frontend build work.

**Step 3: Write minimal implementation**

Refactor `deploy.sh` to:

- keep environment and config preflight
- keep data directory creation
- remove redundant host frontend build steps
- rely on Docker image build as the only build path
- run `docker compose build` and `docker compose up -d`

**Step 4: Run test to verify it passes**

Run: `bash tests/scripts/test_script_consistency.sh`
Expected: PASS

### Task 5: Add the published-image deployment path

**Files:**
- Modify: `Makefile`
- Create: `deploy-image.sh`
- Reference: `docker-compose.prod.yml.example`

**Step 1: Write the failing test**

Extend the regression script to assert that:

- `make -n deploy-image` invokes `./deploy-image.sh`
- `deploy-image.sh` uses `docker compose -f docker-compose.prod.yml pull`
- `deploy-image.sh` uses `docker compose -f docker-compose.prod.yml up -d`

**Step 2: Run test to verify it fails**

Run: `bash tests/scripts/test_script_consistency.sh`
Expected: FAIL because no image deployment entrypoint exists yet.

**Step 3: Write minimal implementation**

Create `deploy-image.sh` to:

- ensure `docker-compose.prod.yml` exists
- ensure `.env` exists, creating from `.env.example` when needed
- ensure `backend/config.prod.yaml` exists, creating from example when needed
- pull published images
- start the stack with `docker compose -f docker-compose.prod.yml up -d`

If compatibility is desired, wire `make prod` to this script without documenting it.

**Step 4: Run test to verify it passes**

Run: `bash tests/scripts/test_script_consistency.sh`
Expected: PASS

### Task 6: Align docs with the approved interface and default user path

**Files:**
- Modify: `README.md`
- Modify: `QUICKSTART.md`
- Modify: `docs/QUICK_REFERENCE.md`

**Step 1: Write the failing test**

Extend the regression script to assert that:

- user-facing docs recommend image deployment for ordinary users
- docs describe `make deploy` as source deployment
- docs describe `make deploy-image` as image deployment
- docs stop advertising `make analyzer`, `make prod`, `make dev-backend`, `make dev-frontend`, and `make dev-ml`

**Step 2: Run test to verify it fails**

Run: `bash tests/scripts/test_script_consistency.sh`
Expected: FAIL because docs still mention outdated targets and deployment guidance.

**Step 3: Write minimal implementation**

Update docs so that:

- `README.md` positions image deployment as the default recommendation
- `QUICKSTART.md` uses the image deployment path first
- `docs/QUICK_REFERENCE.md` reflects the actual public root Make targets

**Step 4: Run test to verify it passes**

Run: `bash tests/scripts/test_script_consistency.sh`
Expected: PASS

### Task 7: Verify the cleaned interface end to end

**Files:**
- No file changes required

**Step 1: Run script regression**

Run: `bash tests/scripts/test_script_consistency.sh`
Expected: PASS

**Step 2: Smoke-check Make output**

Run: `make help`
Expected: only the approved public commands are advertised.

Run: `make -n dev`
Expected: prints `./dev.sh`

Run: `make -n deploy`
Expected: prints `./deploy.sh`

Run: `make -n deploy-image`
Expected: prints `./deploy-image.sh`

**Step 3: Validate compose templates**

Run: `docker compose -f docker-compose.yml.example config >/tmp/relive-source-compose.out`
Expected: success

Run: `docker compose -f docker-compose.prod.yml.example config >/tmp/relive-image-compose.out`
Expected: success

**Step 4: Optional compatibility smoke-check**

Run: `make -n prod`
Expected: either no target exists, or it expands to the same image-deployment path if a deprecated alias is intentionally retained.

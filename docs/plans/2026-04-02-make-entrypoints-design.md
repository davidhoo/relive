# Make Entrypoints Cleanup Design

**Date:** 2026-04-02

## Problem

The current root `Makefile` exposes too many mixed-purpose targets:

- public user commands
- developer convenience commands
- internal helper targets

At the same time:

- `make dev` is interactive instead of deterministic
- `make deploy` means "deploy from source"
- `make prod` means "deploy from published image"
- `deploy.sh` still mixes host-side frontend build work with Docker image build work
- docs no longer match the actual public interface

This makes the project harder to explain to both normal users and contributors.

## Goals

- Keep the root `Makefile` small and intentional
- Make `make dev` the single primary local development entrypoint
- Make deployment modes explicit by source:
  - source deployment
  - image deployment
- Keep `make build-analyzer` at the project root
- Align scripts, docs, and regression checks around the same command contract

## Non-Goals

- No change to backend/frontend application behavior
- No change to analyzer architecture
- No broader startup unification beyond the current local-dev vs deployed-runtime split

## Approved Public Interface

The root `Makefile` should publicly expose:

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

The following should no longer be public entrypoints:

- `make dev-backend`
- `make dev-frontend`
- `make analyzer`
- `make prod`
- `make deps`

Internal helpers may continue to exist if needed, but should not appear in help text or main docs:

- `sync-version`
- `check-compose`

## Command Semantics

### `make dev`

- developer-only command
- non-interactive
- starts the full local development environment
- default behavior is local backend + local frontend

This command optimizes for development speed and fast feedback.

### `make build`

- builds the Docker image from the current source tree
- does not start containers

### `make deploy`

- developer-only deployment path
- deploys from the current source tree
- may perform config validation and runtime directory setup
- must not require redundant host-side frontend builds when Docker already builds the frontend

This command is for maintainers and contributors validating current source changes in a deployed form.

### `make deploy-image`

- user-facing deployment path
- deploys from published images
- does not run local Docker builds
- does not require local Go or Node toolchains

This is the recommended deployment path for ordinary users.

### `make build-analyzer`

- remains at the root because analyzer is a project-level capability
- should continue to build the analyzer binary in a predictable location

## Operational Commands

`make logs`, `make stop`, and `make restart` should remain project-level commands.

To support both deployment modes without multiplying public commands, they should resolve the compose file from local context:

1. prefer `docker-compose.yml` when present
2. otherwise use `docker-compose.prod.yml` when present
3. otherwise fail with a clear setup message

This preserves a small public interface while still supporting both source and image deployments.

## Documentation Rules

Documentation should distinguish between:

- local development: `make dev`
- source deployment: `make deploy`
- image deployment: `make deploy-image`

The recommended user path in primary docs should be image deployment, not source deployment.

## Migration Notes

- `make prod` may be kept temporarily as an undocumented compatibility alias to `make deploy-image`, but it should not remain the preferred name
- `make analyzer` should be removed if it has no real usage
- outdated references such as `make dev-ml` must be removed

## Verification Strategy

Use a lightweight script regression check plus command smoke tests to lock the public contract:

- `bash tests/scripts/test_script_consistency.sh`
- `make help`
- `make -n dev`
- `make -n deploy`
- `make -n deploy-image`
- `docker compose -f docker-compose.yml.example config`
- `docker compose -f docker-compose.prod.yml.example config`

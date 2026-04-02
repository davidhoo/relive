#!/bin/bash

set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
BACKEND_PID=""
FRONTEND_PID=""

stop_process() {
    local pid="$1"

    if [ -z "${pid}" ]; then
        return
    fi

    if kill -0 "${pid}" 2>/dev/null; then
        kill "${pid}" 2>/dev/null || true
    fi

    wait "${pid}" 2>/dev/null || true
}

cleanup() {
    stop_process "${FRONTEND_PID}"
    stop_process "${BACKEND_PID}"
}

trap cleanup EXIT INT TERM

cd "${ROOT}"

echo "Starting local development environment..."

if [ ! -f "backend/config.dev.yaml" ]; then
    if [ -f "backend/config.dev.yaml.example" ]; then
        echo "Creating backend/config.dev.yaml from example..."
        cp backend/config.dev.yaml.example backend/config.dev.yaml
    else
        echo "Missing backend/config.dev.yaml.example"
        exit 1
    fi
fi

mkdir -p backend/data/logs backend/data/photos

if ! command -v go >/dev/null 2>&1; then
    echo "Missing Go runtime"
    exit 1
fi

if ! command -v npm >/dev/null 2>&1; then
    echo "Missing npm"
    exit 1
fi

if [ ! -d "frontend/node_modules" ]; then
    echo "Installing frontend dependencies..."
    (cd frontend && npm install)
fi

echo "Backend:  http://localhost:8080"
echo "Frontend: http://localhost:5173"
echo "Press Ctrl+C to stop both services."

(cd backend && go run cmd/relive/main.go --config config.dev.yaml) &
BACKEND_PID=$!

sleep 3

if ! kill -0 "${BACKEND_PID}" 2>/dev/null; then
    echo "Backend failed to start. Port 8080 may already be in use." >&2
    echo "Stop the existing service and retry make dev." >&2
    exit 1
fi

(cd frontend && npm run dev) &
FRONTEND_PID=$!

wait "${BACKEND_PID}" "${FRONTEND_PID}"

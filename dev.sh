#!/bin/bash

set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
ML_PID=""
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
    stop_process "${ML_PID}"
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
mkdir -p data/backend/logs data/backend/photos data/ml-models

if ! command -v go >/dev/null 2>&1; then
    echo "Missing Go runtime"
    exit 1
fi

if ! command -v npm >/dev/null 2>&1; then
    echo "Missing npm"
    exit 1
fi

if ! command -v python3 >/dev/null 2>&1; then
    echo "Missing python3 runtime"
    exit 1
fi

if [ ! -d "frontend/node_modules" ]; then
    echo "Installing frontend dependencies..."
    (cd frontend && npm install)
fi

if [ ! -x "ml-service/.venv/bin/python" ]; then
    echo "Creating ml-service virtual environment..."
    python3 -m venv ml-service/.venv
fi

if ! ml-service/.venv/bin/python -c "import fastapi, uvicorn, cv2" >/dev/null 2>&1; then
    echo "Installing ml-service dependencies..."
    (cd ml-service && .venv/bin/python -m pip install -r requirements.txt)
fi

echo "ML:       http://localhost:5050"
echo "Backend:  http://localhost:8080"
echo "Frontend: http://localhost:5173"
echo "Press Ctrl+C to stop both services."

(cd ml-service && .venv/bin/python -m uvicorn app.main:app --host 127.0.0.1 --port 5050) &
ML_PID=$!

sleep 2

if ! kill -0 "${ML_PID}" 2>/dev/null; then
    echo "ML service failed to start. Port 5050 may already be in use." >&2
    echo "Stop the existing service and retry make dev." >&2
    exit 1
fi

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

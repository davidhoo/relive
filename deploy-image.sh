#!/bin/bash

set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"

cd "${ROOT}"

if ! command -v docker >/dev/null 2>&1; then
    echo "Missing Docker"
    exit 1
fi

if ! docker compose version >/dev/null 2>&1; then
    echo "Missing docker compose"
    exit 1
fi

if [ ! -f "docker-compose.prod.yml" ]; then
    if [ -f "docker-compose.prod.yml.example" ]; then
        echo "Creating docker-compose.prod.yml from example..."
        cp docker-compose.prod.yml.example docker-compose.prod.yml
    else
        echo "Missing docker-compose.prod.yml.example"
        exit 1
    fi
fi

if [ ! -f ".env" ]; then
    if [ -f ".env.example" ]; then
        echo "Creating .env from example..."
        cp .env.example .env
    else
        echo "Missing .env.example"
        exit 1
    fi
fi

if [ ! -f "backend/config.prod.yaml" ]; then
    if [ -f "backend/config.prod.yaml.example" ]; then
        echo "Creating backend/config.prod.yaml from example..."
        cp backend/config.prod.yaml.example backend/config.prod.yaml
    else
        echo "Missing backend/config.prod.yaml.example"
        exit 1
    fi
fi

mkdir -p data/backend/logs data/backend/thumbnails

docker compose -f docker-compose.prod.yml pull
docker compose -f docker-compose.prod.yml up -d

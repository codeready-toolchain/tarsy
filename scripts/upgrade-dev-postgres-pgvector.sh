#!/usr/bin/env bash
#
# Upgrade local dev PostgreSQL container from postgres:17-alpine to
# pgvector/pgvector:pg17. Required for the investigation_memories migration
# (20260324000000) which needs the pgvector extension.
#
# Data is preserved — both images use PostgreSQL 17, the new one just
# includes the pgvector library files.
#
# Usage:
#   ./scripts/upgrade-dev-postgres-pgvector.sh
#
# After running this script, start the app normally with `make dev`.
# The pending migration will apply automatically.

set -euo pipefail

CONTAINER_NAME="tarsy-postgres"
NEW_IMAGE="mirror.gcr.io/pgvector/pgvector:pg17"
COMPOSE_FILE="deploy/podman-compose.yml"

cd "$(git rev-parse --show-toplevel)"

current_image=$(podman inspect "$CONTAINER_NAME" --format '{{.ImageName}}' 2>/dev/null || true)

if [[ "$current_image" == *"pgvector"* ]]; then
    echo "✅ Container '$CONTAINER_NAME' already uses a pgvector image ($current_image). Nothing to do."
    exit 0
fi

if [[ -z "$current_image" ]]; then
    echo "ℹ️  No container '$CONTAINER_NAME' found. It will be created with the pgvector image on next 'make db-start'."
    exit 0
fi

echo "Current image: $current_image"
echo "Target image:  $NEW_IMAGE"
echo ""
echo "Stopping container..."
podman stop "$CONTAINER_NAME" 2>/dev/null || true

echo "Removing container (volume is preserved)..."
podman rm "$CONTAINER_NAME" 2>/dev/null || true

echo "Pulling new image..."
podman pull "$NEW_IMAGE"

echo "Starting container with new image..."
COMPOSE_PROJECT_NAME=tarsy podman compose -f "$COMPOSE_FILE" up -d postgres

echo "Waiting for PostgreSQL to be ready..."
until podman exec "$CONTAINER_NAME" pg_isready -U tarsy >/dev/null 2>&1; do
    sleep 1
done

echo ""
echo "✅ Done! Container '$CONTAINER_NAME' now uses: $(podman inspect "$CONTAINER_NAME" --format '{{.ImageName}}')"
echo ""
echo "Run 'make dev' to start the app. The pending migration will apply automatically."

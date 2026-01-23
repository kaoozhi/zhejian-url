#!/bin/sh
set -e

# Wait for PostgreSQL
until pg_isready -h postgres -U ${POSTGRES_USER:-zhejian}; do
    sleep 2
done

# Run migrations
migrate -path=/migrations -database="${DATABASE_URL}" up
echo "Migrations completed!"
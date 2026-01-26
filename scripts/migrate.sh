#!/bin/bash

# Database Migration Runner
# Usage: ./scripts/migrate.sh

set -e

DB_CONTAINER="zhejian-postgres"
DB_USER="zhejian"
DB_NAME="urlshortener"
MIGRATIONS_DIR="$(dirname "$0")/../migrations/schema"

echo "🔄 Running database migrations..."

# Create migrations tracking table if it doesn't exist
docker exec -i $DB_CONTAINER psql -U $DB_USER -d $DB_NAME <<EOSQL
CREATE TABLE IF NOT EXISTS schema_migrations (
    id SERIAL PRIMARY KEY,
    migration_name VARCHAR(255) UNIQUE NOT NULL,
    applied_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
EOSQL

# Run each migration file in order
for migration in $(ls $MIGRATIONS_DIR/*.sql 2>/dev/null | sort); do
    filename=$(basename $migration)
    
    # Check if migration was already applied
    applied=$(docker exec -i $DB_CONTAINER psql -U $DB_USER -d $DB_NAME -t -c \
        "SELECT COUNT(*) FROM schema_migrations WHERE migration_name = '$filename';")
    
    if [ $(echo $applied | tr -d ' ') -eq 0 ]; then
        echo "⚡ Applying: $filename"
        docker exec -i $DB_CONTAINER psql -U $DB_USER -d $DB_NAME < $migration
        
        # Record that migration was applied
        docker exec -i $DB_CONTAINER psql -U $DB_USER -d $DB_NAME -c \
            "INSERT INTO schema_migrations (migration_name) VALUES ('$filename');"
        echo "✅ Applied: $filename"
    else
        echo "⏭️  Skipped: $filename (already applied)"
    fi
done

echo "✅ All migrations completed!"

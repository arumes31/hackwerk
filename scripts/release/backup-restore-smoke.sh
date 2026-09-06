#!/bin/sh
set -eu

suffix="$$-$(openssl rand -hex 8)"
audit_label="hackwerk-restore-$suffix"
network="hackwerk-restore-$suffix"
postgres="hackwerk-restore-db-$suffix"
image=${HACKWERK_RELEASE_IMAGE:-hackwerk-scan:local}
tools_image="hackwerk-postgres-tools:$suffix"
network_id=""
postgres_id=""
tools_image_id=""

cleanup() {
  if [ -n "$postgres_id" ] && [ "$(docker inspect --format '{{index .Config.Labels "hackwerk.audit"}}' "$postgres_id" 2>/dev/null || true)" = "$audit_label" ]; then
    docker rm -f "$postgres_id" >/dev/null 2>&1 || true
  fi
  if [ -n "$network_id" ] && [ "$(docker network inspect --format '{{index .Labels "hackwerk.audit"}}' "$network_id" 2>/dev/null || true)" = "$audit_label" ]; then
    docker network rm "$network_id" >/dev/null 2>&1 || true
  fi
  if [ -n "$tools_image_id" ] && [ "$(docker image inspect --format '{{.Id}}' "$tools_image" 2>/dev/null || true)" = "$tools_image_id" ]; then
    docker image rm "$tools_image" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT HUP INT TERM

docker inspect "$postgres" >/dev/null 2>&1 && { echo "Containername bereits belegt" >&2; exit 1; }
docker network inspect "$network" >/dev/null 2>&1 && { echo "Netzwerkname bereits belegt" >&2; exit 1; }
docker image inspect "$tools_image" >/dev/null 2>&1 && { echo "Imagetag bereits belegt" >&2; exit 1; }
docker build --build-arg "HACKWERK_RELEASE_IMAGE=$image" -f scripts/release/postgres-tools.Dockerfile -t "$tools_image" . >/dev/null
tools_image_id=$(docker image inspect --format '{{.Id}}' "$tools_image")
network_id=$(docker network create --label "hackwerk.audit=$audit_label" "$network")
postgres_id=$(docker run -d --name "$postgres" --label "hackwerk.audit=$audit_label" --network "$network" \
  -e POSTGRES_DB=postgres -e POSTGRES_USER=postgres -e POSTGRES_PASSWORD=restore-test-only \
  postgres:18.6-alpine@sha256:d3e1620b530c944afa6e887d22eb899824da68e19c52024bf98f5220c88a65b2)

attempt=0
until docker exec "$postgres" pg_isready -U postgres -d postgres >/dev/null 2>&1; do
  attempt=$((attempt + 1))
  [ "$attempt" -lt 60 ] || { echo "Restoretest-Datenbank wurde nicht bereit" >&2; exit 1; }
  sleep 1
done

docker exec "$postgres" psql -U postgres -d postgres -v ON_ERROR_STOP=1 \
  -c "CREATE ROLE hackwerk_migrate LOGIN PASSWORD 'migration-test-only'" \
  -c "CREATE ROLE hackwerk_app LOGIN PASSWORD 'runtime-test-only'" \
  -c "CREATE DATABASE hackwerk_source" >/dev/null

admin_url="postgres://postgres:restore-test-only@$postgres:5432/postgres?sslmode=disable"
source_url="postgres://postgres:restore-test-only@$postgres:5432/hackwerk_source?sslmode=disable"
restore_url="postgres://hackwerk_migrate:migration-test-only@$postgres:5432/hackwerk_restore_test_$suffix?sslmode=disable"

docker run --rm --network "$network" -e APP_ENV=test -e DATABASE_URL="$source_url" "$image" migrate up >/dev/null
docker run --rm --network "$network" -e APP_ENV=development -e DATABASE_URL="$source_url" "$image" seed-dev >/dev/null
source_customers=$(docker exec "$postgres" psql -U postgres -d hackwerk_source -Atc "SELECT count(*) FROM customers")
[ "$source_customers" -gt 0 ] || { echo "Backup-Fixture enthält keine Kunden" >&2; exit 1; }
docker run --rm --network "$network" \
  -e SOURCE_DATABASE_URL="$source_url" \
  -e RESTORE_ADMIN_DATABASE_URL="$admin_url" \
  -e RESTORE_TEST_DATABASE_URL="$restore_url" \
  -e RESTORE_TEST_DATABASE="hackwerk_restore_test_$suffix" \
  -e RESTORE_OWNER_ROLE=hackwerk_migrate \
  -e KEEP_RESTORE_TEST_DB=true \
  "$tools_image"

docker run --rm --network "$network" -e APP_ENV=test -e DATABASE_URL="$restore_url" "$image" migrate status >/dev/null
docker run --rm --network "$network" -e APP_ENV=test -e DATABASE_URL="$restore_url" "$image" migrate down >/dev/null
docker run --rm --network "$network" -e APP_ENV=test -e DATABASE_URL="$restore_url" "$image" migrate up >/dev/null

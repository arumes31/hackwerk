#!/bin/sh
set -eu

suffix="$$-$(openssl rand -hex 8)"
audit_label="hackwerk-migration-$suffix"
network="hackwerk-migration-$suffix"
postgres="hackwerk-migration-db-$suffix"
image=${HACKWERK_RELEASE_IMAGE:-hackwerk-scan:local}
network_id=""
postgres_id=""
expected_version=$(docker run --rm "$image" schema-version)
case "$expected_version" in
  *[!0-9]*|'') echo "Release-Image lieferte keine gültige Schemaversion" >&2; exit 1;;
esac

cleanup() {
  if [ -n "$postgres_id" ] && [ "$(docker inspect --format '{{index .Config.Labels "hackwerk.audit"}}' "$postgres_id" 2>/dev/null || true)" = "$audit_label" ]; then
    docker rm -f "$postgres_id" >/dev/null 2>&1 || true
  fi
  if [ -n "$network_id" ] && [ "$(docker network inspect --format '{{index .Labels "hackwerk.audit"}}' "$network_id" 2>/dev/null || true)" = "$audit_label" ]; then
    docker network rm "$network_id" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT HUP INT TERM

docker inspect "$postgres" >/dev/null 2>&1 && { echo "Containername bereits belegt" >&2; exit 1; }
docker network inspect "$network" >/dev/null 2>&1 && { echo "Netzwerkname bereits belegt" >&2; exit 1; }
network_id=$(docker network create --label "hackwerk.audit=$audit_label" "$network")
postgres_id=$(docker run -d --name "$postgres" --label "hackwerk.audit=$audit_label" --network "$network" \
  -e POSTGRES_DB=hackwerk_migration -e POSTGRES_USER=hackwerk -e POSTGRES_PASSWORD=migration-test-only \
  postgres:18.6-alpine@sha256:d3e1620b530c944afa6e887d22eb899824da68e19c52024bf98f5220c88a65b2)

attempt=0
until docker exec "$postgres" pg_isready -U hackwerk -d hackwerk_migration >/dev/null 2>&1; do
  attempt=$((attempt + 1))
  [ "$attempt" -lt 60 ] || { echo "Migrationstest-Datenbank wurde nicht bereit" >&2; exit 1; }
  sleep 1
done

database_url="postgres://hackwerk:migration-test-only@$postgres:5432/hackwerk_migration?sslmode=disable"
run_migration() {
  docker run --rm --network "$network" -e APP_ENV=test -e DATABASE_URL="$database_url" "$image" migrate "$1" >/dev/null
}

run_migration up
run_migration up
run_migration status
docker run --rm --network "$network" -e APP_ENV=development -e DATABASE_URL="$database_url" "$image" seed-dev >/dev/null
customers_before=$(docker exec "$postgres" psql -U hackwerk -d hackwerk_migration -Atc "SELECT count(*) FROM customers")
[ "$customers_before" -gt 0 ] || { echo "Upgrade-Fixture enthält keine Kunden" >&2; exit 1; }
run_migration down
run_migration up

customers_after=$(docker exec "$postgres" psql -U hackwerk -d hackwerk_migration -Atc "SELECT count(*) FROM customers")
[ "$customers_after" = "$customers_before" ] || { echo "Upgrade hat Bestandsdaten verändert" >&2; exit 1; }

version=$(docker exec "$postgres" psql -U hackwerk -d hackwerk_migration -Atc "SELECT value FROM schema_metadata WHERE key='application_schema_version'")
[ "$version" = "$expected_version" ] || { echo "Unerwartete Schema-Version: $version (Binary erwartet $expected_version)" >&2; exit 1; }
printf 'Fresh-/Upgrade-Migrationstest erfolgreich; Schema %s.\n' "$version"

#!/bin/sh
set -eu

suffix=$$
network="hackwerk-migration-$suffix"
postgres="hackwerk-migration-db-$suffix"
image=${HACKWERK_RELEASE_IMAGE:-hackwerk-scan:local}

cleanup() {
  docker rm -f "$postgres" >/dev/null 2>&1 || true
  docker network rm "$network" >/dev/null 2>&1 || true
}
trap cleanup EXIT HUP INT TERM

docker network create "$network" >/dev/null
docker run -d --name "$postgres" --network "$network" \
  -e POSTGRES_DB=hackwerk_migration -e POSTGRES_USER=hackwerk -e POSTGRES_PASSWORD=migration-test-only \
  postgres:18.6-alpine >/dev/null

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
[ "$version" = 10 ] || { echo "Unerwartete Schema-Version: $version" >&2; exit 1; }
printf 'Fresh-/Upgrade-Migrationstest erfolgreich; Schema %s.\n' "$version"

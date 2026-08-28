#!/bin/sh
set -eu

suffix=$$
network="hackwerk-container-$suffix"
postgres="hackwerk-container-db-$suffix"
app="hackwerk-container-app-$suffix"
image=${HACKWERK_RELEASE_IMAGE:-hackwerk-scan:local}

cleanup() {
  docker rm -f "$app" "$postgres" >/dev/null 2>&1 || true
  docker network rm "$network" >/dev/null 2>&1 || true
}
trap cleanup EXIT HUP INT TERM

docker network create "$network" >/dev/null
docker run -d --name "$postgres" --network "$network" \
  -e POSTGRES_DB=hackwerk -e POSTGRES_USER=hackwerk -e POSTGRES_PASSWORD=container-test-only \
  postgres:18.6-alpine@sha256:d3e1620b530c944afa6e887d22eb899824da68e19c52024bf98f5220c88a65b2 >/dev/null

attempt=0
until docker exec "$postgres" pg_isready -U hackwerk -d hackwerk >/dev/null 2>&1; do
  attempt=$((attempt + 1))
  [ "$attempt" -lt 60 ] || { echo "Container-Smoke-Datenbank wurde nicht bereit" >&2; exit 1; }
  sleep 1
done

database_url="postgres://hackwerk:container-test-only@$postgres:5432/hackwerk?sslmode=disable"
docker run --rm --network "$network" -e APP_ENV=test -e DATABASE_URL="$database_url" "$image" migrate up >/dev/null
docker run --rm --network "$network" -e APP_ENV=development -e DATABASE_URL="$database_url" "$image" seed-dev >/dev/null

docker run -d --name "$app" --network "$network" --read-only --user 65532:65532 \
  --cap-drop ALL --security-opt no-new-privileges --pids-limit 128 \
  --tmpfs /tmp:size=64m,mode=1777,noexec,nosuid,nodev \
  -e APP_ENV=test -e APP_BASE_URL=http://127.0.0.1:18533 \
  -e APP_LISTEN_ADDR=:18533 -e DATABASE_URL="$database_url" "$image" serve >/dev/null

attempt=0
healthcheck_output=""
until healthcheck_output=$(MSYS_NO_PATHCONV=1 docker exec "$app" /hackwerk healthcheck 2>&1); do
  attempt=$((attempt + 1))
  [ "$attempt" -lt 60 ] || {
    printf '%s\n' "$healthcheck_output" >&2
    docker logs "$app" >&2
    exit 1
  }
  sleep 1
done

[ "$(docker inspect --format '{{.Config.User}}' "$app")" = "65532:65532" ] || { echo "Container läuft nicht als non-root" >&2; exit 1; }
[ "$(docker inspect --format '{{.HostConfig.ReadonlyRootfs}}' "$app")" = true ] || { echo "Root-Dateisystem ist beschreibbar" >&2; exit 1; }
[ "$(docker inspect --format '{{json .HostConfig.CapDrop}}' "$app")" = '["ALL"]' ] || { echo "Capabilities sind nicht vollständig entfernt" >&2; exit 1; }
[ "$(docker inspect --format '{{json .HostConfig.SecurityOpt}}' "$app")" = '["no-new-privileges"]' ] || { echo "no-new-privileges fehlt" >&2; exit 1; }

printf 'Container-Smoke erfolgreich: Port 18533, non-root, read-only, Demo-Seed und Healthcheck.\n'

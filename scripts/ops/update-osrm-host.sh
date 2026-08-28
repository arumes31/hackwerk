#!/bin/sh
set -eu

deployment_dir=${HACKWERK_DEPLOYMENT_DIR:-/container/hackwerk}
compose_file=${HACKWERK_COMPOSE_FILE:-compose.yaml}
data_dir=${OSRM_DATA_DIR:-$deployment_dir/.tmp/osrm-data}
osrm_image=${OSRM_IMAGE:-hackwerk-osrm-tools:v26.7.3-1}

case "$deployment_dir" in
    /*) ;;
    *)
        printf '%s\n' "HACKWERK_DEPLOYMENT_DIR must be absolute" >&2
        exit 1
        ;;
esac
case "$data_dir" in
    /*) ;;
    *)
        printf '%s\n' "OSRM_DATA_DIR must be absolute" >&2
        exit 1
        ;;
esac

wait_for_healthy_osrm() {
    attempt=0
    while [ "$attempt" -lt 60 ]; do
        container_id=$(docker compose -f "$compose_file" --profile routing ps -q osrm)
        if [ -n "$container_id" ]; then
            health=$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$container_id" 2>/dev/null || true)
            case "$health" in
                healthy) return 0 ;;
                unhealthy|exited|dead) return 1 ;;
            esac
        fi
        attempt=$((attempt + 1))
        sleep 2
    done
    return 1
}

cd "$deployment_dir"
if [ -L "$data_dir" ]; then
    printf '%s\n' "OSRM_DATA_DIR must not be a symbolic link" >&2
    exit 1
fi
install -d -m 0750 -o 65532 -g 65532 "$data_dir"
export OSRM_DATA_DIR="$data_dir"
export OSRM_IMAGE="$osrm_image"

if ! docker image inspect "$osrm_image" >/dev/null 2>&1; then
    printf '%s\n' "OSRM_IMAGE is not loaded on this host: $osrm_image" >&2
    exit 1
fi
docker compose -f "$compose_file" --profile osrm-ops run --rm osrm-update
if docker compose -f "$compose_file" --profile routing up -d --no-deps --force-recreate osrm && wait_for_healthy_osrm; then
    docker compose -f "$compose_file" --profile osrm-ops run --rm osrm-update prune
    exit 0
fi

printf '%s\n' "new OSRM generation is unhealthy; restoring previous generation" >&2
if ! docker compose -f "$compose_file" --profile osrm-ops run --rm osrm-update rollback; then
    printf '%s\n' "no valid previous OSRM generation is available" >&2
    exit 1
fi
if ! docker compose -f "$compose_file" --profile routing up -d --no-deps --force-recreate osrm || ! wait_for_healthy_osrm; then
    printf '%s\n' "previous OSRM generation also failed health validation" >&2
fi
exit 1

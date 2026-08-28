#!/bin/sh
set -eu

data_root=${OSRM_DATA_ROOT:-/data}
bbox=${OSRM_BBOX:-9.4,46.3,17.5,49.5}
min_free_kb=${OSRM_MIN_FREE_KB:-20971520}
threads=${OSRM_THREADS:-2}
download_limit=${OSRM_DOWNLOAD_LIMIT:-4M}
mode=${1:-update}
download_dir="$data_root/downloads"
generation_dir="$data_root/generations"
staging_root="$data_root/staging"
candidate="$staging_root/candidate.$$"
route_base="$candidate/route.osrm"
server_pid=
snapshot_timestamp=

cleanup() {
    if [ -n "$server_pid" ]; then
        kill "$server_pid" 2>/dev/null || true
        wait "$server_pid" 2>/dev/null || true
    fi
    if [ -d "$candidate" ]; then
        rm -rf -- "$candidate"
    fi
}
trap cleanup EXIT INT TERM

case "$mode" in
    update|rollback|prune) ;;
    *)
        printf '%s\n' "usage: hackwerk-osrm-update [update|rollback|prune]" >&2
        exit 1
        ;;
esac
case "$threads" in
    1|2|3|4) ;;
    *)
        printf '%s\n' "OSRM_THREADS must be between 1 and 4" >&2
        exit 1
        ;;
esac
case "$download_limit" in
    1M|2M|4M|8M) ;;
    *)
        printf '%s\n' "OSRM_DOWNLOAD_LIMIT must be 1M, 2M, 4M or 8M" >&2
        exit 1
        ;;
esac
if [ "$mode" = update ] && [ "$bbox" != "9.4,46.3,17.5,49.5" ]; then
    printf '%s\n' "OSRM_BBOX must remain the reviewed Austria-border corridor 9.4,46.3,17.5,49.5" >&2
    exit 1
fi

mkdir -p "$download_dir" "$generation_dir" "$staging_root"
exec 9>"$data_root/update.lock"
if ! flock -n 9; then
    printf '%s\n' "another OSRM update is already running" >&2
    exit 1
fi

valid_generation_target() {
    target=$1
    case "$target" in
        generations/*)
            [ "$target" = "generations/$(basename "$target")" ] && [ -d "$data_root/$target" ]
            ;;
        *) return 1 ;;
    esac
}

if [ "$mode" = rollback ]; then
    current_target=$(readlink "$data_root/current" 2>/dev/null || true)
    previous_target=$(readlink "$data_root/previous" 2>/dev/null || true)
    if ! valid_generation_target "$current_target" || ! valid_generation_target "$previous_target"; then
        printf '%s\n' "current and previous must reference valid OSRM generations" >&2
        exit 1
    fi
    ln -s "$previous_target" "$data_root/current.next.$$"
    mv -Tf -- "$data_root/current.next.$$" "$data_root/current"
    ln -s "$current_target" "$data_root/previous.next.$$"
    mv -Tf -- "$data_root/previous.next.$$" "$data_root/previous"
    printf '%s\n' "rolled OSRM back to $previous_target"
    exit 0
fi

if [ "$mode" = prune ]; then
    current_target=$(readlink "$data_root/current" 2>/dev/null || true)
    previous_target=$(readlink "$data_root/previous" 2>/dev/null || true)
    if ! valid_generation_target "$current_target"; then
        printf '%s\n' "current must reference a valid OSRM generation" >&2
        exit 1
    fi
    if [ -n "$previous_target" ] && ! valid_generation_target "$previous_target"; then
        printf '%s\n' "previous must reference a valid OSRM generation" >&2
        exit 1
    fi
    for generation_path in "$generation_dir"/*; do
        [ -d "$generation_path" ] || continue
        generation_target="generations/$(basename "$generation_path")"
        if [ "$generation_target" != "$current_target" ] && [ "$generation_target" != "$previous_target" ]; then
            rm -rf -- "$generation_path"
        fi
    done
    printf '%s\n' "retained current and previous OSRM generations"
    exit 0
fi

available_kb=$(df -Pk "$data_root" | awk 'NR == 2 {print $4}')
case "$available_kb" in
    ''|*[!0-9]*)
        printf '%s\n' "could not determine free space for $data_root" >&2
        exit 1
        ;;
esac
if [ "$available_kb" -lt "$min_free_kb" ]; then
    printf '%s\n' "OSRM update needs at least $min_free_kb KiB free; $available_kb KiB available" >&2
    exit 1
fi

mkdir "$candidate"

download_source() {
    source_name=$1
    source_url=$2
    source_file="$download_dir/$source_name-latest.osm.pbf"
    checksum_file="$download_dir/$source_name-latest.osm.pbf.md5"
    checksum_next="$checksum_file.next"

    curl --fail --location --silent --show-error \
        --proto '=https' --tlsv1.2 --retry 5 --retry-all-errors \
        --limit-rate "$download_limit" \
        --connect-timeout 30 --max-time 7200 \
        "$source_url.md5" --output "$checksum_next"

    if [ -f "$source_file" ] && [ -f "$checksum_file" ] && cmp -s "$checksum_next" "$checksum_file"; then
        rm -f -- "$checksum_next"
    else
        curl --fail --location --silent --show-error \
            --proto '=https' --tlsv1.2 --retry 5 --retry-all-errors \
            --limit-rate "$download_limit" \
            --connect-timeout 30 --max-time 21600 \
            "$source_url" --output "$source_file.part"
        expected=$(awk 'NR == 1 {print $1}' "$checksum_next")
        actual=$(md5sum "$source_file.part" | awk '{print $1}')
        if [ -z "$expected" ] || [ "$actual" != "$expected" ]; then
            rm -f -- "$source_file.part" "$checksum_next"
            printf '%s\n' "checksum verification failed for $source_name" >&2
            exit 1
        fi
        mv -f -- "$source_file.part" "$source_file"
        mv -f -- "$checksum_next" "$checksum_file"
    fi

    source_timestamp=$(osmium fileinfo -g header.option.osmosis_replication_timestamp "$source_file")
    if [ -z "$source_timestamp" ]; then
        printf '%s\n' "missing OSM replication timestamp for $source_name" >&2
        exit 1
    fi
    if [ -z "$snapshot_timestamp" ]; then
        snapshot_timestamp=$source_timestamp
    elif [ "$snapshot_timestamp" != "$source_timestamp" ]; then
        printf '%s\n' "regional OSM sources do not share one replication timestamp" >&2
        exit 1
    fi

    osmium extract --overwrite --strategy=complete_ways --bbox "$bbox" \
        "$source_file" --output "$candidate/$source_name.osm.pbf"
}

download_source austria https://download.geofabrik.de/europe/austria-latest.osm.pbf
download_source bavaria https://download.geofabrik.de/europe/germany/bayern-latest.osm.pbf
download_source czech-republic https://download.geofabrik.de/europe/czech-republic-latest.osm.pbf

osmium merge --overwrite \
    "$candidate/austria.osm.pbf" \
    "$candidate/bavaria.osm.pbf" \
    "$candidate/czech-republic.osm.pbf" \
    --output "$candidate/corridor.osm.pbf"
rm -f -- "$candidate/austria.osm.pbf" "$candidate/bavaria.osm.pbf" "$candidate/czech-republic.osm.pbf"

osrm-extract --threads "$threads" --profile /opt/car.lua --output "$route_base" "$candidate/corridor.osm.pbf"
rm -f -- "$candidate/corridor.osm.pbf"
osrm-partition --threads "$threads" "$route_base"
osrm-customize --threads "$threads" "$route_base"
osrm-routed --threads "$threads" --algorithm mld --mmap --trial "$route_base"

osrm-routed --threads "$threads" --algorithm mld --mmap --ip 127.0.0.1 --port 5001 "$route_base" >"$candidate/validation.log" 2>&1 &
server_pid=$!

ready=false
attempt=0
while [ "$attempt" -lt 60 ]; do
    if curl --fail --silent --max-time 2 \
        'http://127.0.0.1:5001/route/v1/driving/14.2858,48.3069;14.3000,48.3100?overview=false' \
        | grep -q '"code":"Ok"'; then
        ready=true
        break
    fi
    if ! kill -0 "$server_pid" 2>/dev/null; then
        printf '%s\n' "candidate OSRM process exited during validation" >&2
        exit 1
    fi
    attempt=$((attempt + 1))
    sleep 1
done
if [ "$ready" != true ]; then
    printf '%s\n' "candidate OSRM process did not become ready" >&2
    exit 1
fi

validate_route() {
    route_name=$1
    coordinates=$2
    response=$(curl --fail --silent --show-error --max-time 30 \
        "http://127.0.0.1:5001/route/v1/driving/$coordinates?overview=full&geometries=polyline6")
    if ! printf '%s' "$response" | grep -q '"code":"Ok"'; then
        printf '%s\n' "OSRM validation route failed: $route_name" >&2
        exit 1
    fi
    if ! printf '%s' "$response" | grep -Eq '"distance":[1-9][0-9]*(\.[0-9]+)?' ||
        ! printf '%s' "$response" | grep -Eq '"duration":[1-9][0-9]*(\.[0-9]+)?' ||
        ! printf '%s' "$response" | grep -Eq '"geometry":"[^" ]{10,}"'; then
        printf '%s\n' "OSRM validation route has no positive road metrics or geometry: $route_name" >&2
        exit 1
    fi
}

validate_route austria '14.2858,48.3069;16.3738,48.2082'
validate_route bavaria '12.1316,47.8564;13.4319,48.5667'
validate_route czechia '14.4747,48.9745;16.6068,49.1951'
validate_route austria-bavaria '13.4319,48.5667;14.2858,48.3069'
validate_route austria-czechia '16.3738,48.2082;16.6068,49.1951'

kill "$server_pid"
wait "$server_pid" || true
server_pid=
rm -f -- "$candidate/validation.log"

generation=$(date -u '+%Y%m%dT%H%M%SZ')
if [ -e "$generation_dir/$generation" ]; then
    generation="$generation-$$"
fi

{
    printf 'generated_at=%s\n' "$generation"
    printf 'bbox=%s\n' "$bbox"
    printf 'profile=car\n'
    printf 'osm_replication_timestamp=%s\n' "$snapshot_timestamp"
    for checksum in "$download_dir"/*.md5; do
        printf '%s=' "$(basename "$checksum" .md5)"
        awk 'NR == 1 {print $1}' "$checksum"
    done
} >"$candidate/manifest.txt"

mv -- "$candidate" "$generation_dir/$generation"
candidate=

old_current=
if [ -L "$data_root/current" ]; then
    old_current=$(readlink "$data_root/current")
fi
if [ -n "$old_current" ] && [ -d "$data_root/$old_current" ]; then
    ln -s "$old_current" "$data_root/previous.next.$$"
    mv -Tf -- "$data_root/previous.next.$$" "$data_root/previous"
fi
ln -s "generations/$generation" "$data_root/current.next.$$"
mv -Tf -- "$data_root/current.next.$$" "$data_root/current"

printf '%s\n' "activated OSRM generation $generation for bbox $bbox"

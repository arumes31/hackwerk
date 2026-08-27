#!/bin/sh
set -eu

minimum=${COVERAGE_MIN:-80.0}
profile=${COVERAGE_PROFILE:-.tmp/coverage/core.out}

case "$minimum" in
  ''|*[!0-9.]*|*.*.*)
    printf 'COVERAGE_MIN muss eine nicht-negative Zahl sein, erhalten: %s\n' "$minimum" >&2
    exit 2
    ;;
esac

profile_dir=$(dirname "$profile")
mkdir -p "$profile_dir"

packages='
./internal/appointment
./internal/auth
./internal/calendarfeed
./internal/customers
./internal/dashboard
./internal/driver
./internal/geocode
./internal/logging
./internal/maptile
./internal/notification
./internal/observability
./internal/outbound
./internal/planning
./internal/resource
./internal/routelocation
./internal/voice
./internal/web'

# The package list intentionally excludes generated sqlc/templ output, process
# entrypoints, migration embedding, and PostgreSQL adapters covered separately
# by the integration suite.
# shellcheck disable=SC2086
go test -count=1 -covermode=atomic -coverprofile "$profile" $packages

actual=$(go tool cover -func "$profile" | awk '/^total:/ {gsub(/%/, "", $3); print $3}')
if [ -z "$actual" ]; then
  printf 'Coverage-Gesamtwert konnte nicht aus %s gelesen werden.\n' "$profile" >&2
  exit 1
fi

printf 'Kernlogik-Coverage: %s%% (Minimum %s%%)\n' "$actual" "$minimum"
awk -v actual="$actual" -v minimum="$minimum" 'BEGIN { exit !(actual + 0 >= minimum + 0) }' || {
  printf 'Coverage-Gate fehlgeschlagen: %s%% liegt unter %s%%.\n' "$actual" "$minimum" >&2
  exit 1
}

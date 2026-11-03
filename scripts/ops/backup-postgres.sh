#!/bin/sh
set -eu
umask 077
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
. "$script_dir/lib/pg-credentials.sh"
trap pg_credentials_cleanup EXIT

: "${DATABASE_URL_FILE:?DATABASE_URL_FILE ist erforderlich}"
: "${BACKUP_DIR:?BACKUP_DIR ist erforderlich}"

case "$BACKUP_DIR" in /*) ;; *) echo "BACKUP_DIR muss absolut sein" >&2; exit 2;; esac
mkdir -p -- "$BACKUP_DIR"
backup_dir=$(CDPATH= cd -- "$BACKUP_DIR" && pwd -P)
case "$backup_dir" in /|/bin|/boot|/dev|/etc|/home|/proc|/root|/run|/sys|/tmp|/usr|/var) echo "BACKUP_DIR ist zu breit" >&2; exit 2;; esac

database_url=$(cat "$DATABASE_URL_FILE")
pg_connection_prepare source "$database_url"
unset database_url
timestamp=$(date -u +%Y%m%dT%H%M%SZ)
target="$backup_dir/hackwerk-$timestamp.dump"
partial="$target.partial"
trap 'rm -f -- "$partial"; pg_credentials_cleanup' EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

pg_run source pg_dump --format=custom --compress=9 --no-owner --no-acl --file="$partial"
chmod 600 "$partial"
mv -- "$partial" "$target"
checksum=$(sha256sum "$target" | awk '{print $1}')
printf '%s  %s\n' "$checksum" "$(basename "$target")" > "$target.sha256"
chmod 600 "$target.sha256"
trap - EXIT HUP INT TERM
pg_credentials_cleanup
printf '%s\n' "$target"

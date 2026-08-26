#!/bin/sh
set -eu
umask 077

: "${DATABASE_URL_FILE:?DATABASE_URL_FILE ist erforderlich}"
: "${BACKUP_DIR:?BACKUP_DIR ist erforderlich}"

case "$BACKUP_DIR" in /*) ;; *) echo "BACKUP_DIR muss absolut sein" >&2; exit 2;; esac
mkdir -p -- "$BACKUP_DIR"
backup_dir=$(CDPATH= cd -- "$BACKUP_DIR" && pwd -P)
case "$backup_dir" in /|/bin|/boot|/dev|/etc|/home|/proc|/root|/run|/sys|/tmp|/usr|/var) echo "BACKUP_DIR ist zu breit" >&2; exit 2;; esac

database_url=$(cat "$DATABASE_URL_FILE")
timestamp=$(date -u +%Y%m%dT%H%M%SZ)
target="$backup_dir/hackwerk-$timestamp.dump"
partial="$target.partial"
trap 'rm -f -- "$partial"' EXIT HUP INT TERM

pg_dump --dbname="$database_url" --format=custom --compress=9 --no-owner --no-acl --file="$partial"
chmod 600 "$partial"
mv -- "$partial" "$target"
checksum=$(sha256sum "$target" | awk '{print $1}')
printf '%s  %s\n' "$checksum" "$(basename "$target")" > "$target.sha256"
chmod 600 "$target.sha256"
trap - EXIT HUP INT TERM
printf '%s\n' "$target"

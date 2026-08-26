#!/bin/sh
set -eu

: "${BACKUP_DIR:?BACKUP_DIR ist erforderlich}"
retention_days=${BACKUP_RETENTION_DAYS:-30}
dry_run=${BACKUP_ROTATION_DRY_RUN:-true}
case "$retention_days" in ''|*[!0-9]*) echo "BACKUP_RETENTION_DAYS muss eine Zahl sein" >&2; exit 2;; esac
case "$BACKUP_DIR" in /*) ;; *) echo "BACKUP_DIR muss absolut sein" >&2; exit 2;; esac
backup_dir=$(CDPATH= cd -- "$BACKUP_DIR" && pwd -P)
case "$backup_dir" in /|/bin|/boot|/dev|/etc|/home|/proc|/root|/run|/sys|/tmp|/usr|/var) echo "BACKUP_DIR ist zu breit" >&2; exit 2;; esac

if [ "$dry_run" = true ]; then
  find "$backup_dir" -maxdepth 1 -type f \( -name 'hackwerk-*.dump' -o -name 'hackwerk-*.dump.sha256' \) -mtime "+$retention_days" -print
  exit 0
fi
[ "$dry_run" = false ] || { echo "BACKUP_ROTATION_DRY_RUN muss true oder false sein" >&2; exit 2; }
find "$backup_dir" -maxdepth 1 -type f \( -name 'hackwerk-*.dump' -o -name 'hackwerk-*.dump.sha256' \) -mtime "+$retention_days" -delete

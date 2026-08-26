#!/bin/sh
set -eu

prefix="${HACKWERK_VERSION_PREFIX:-0.1}"
commit_count="$(git rev-list --count HEAD)"

case "$commit_count" in
  ''|*[!0-9]*)
    printf '%s\n' 'Die Git-Commit-Anzahl ist ungültig.' >&2
    exit 1
    ;;
esac

printf '%s.%s\n' "$prefix" "$commit_count"

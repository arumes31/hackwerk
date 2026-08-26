#!/bin/sh
set -eu

before="$(mktemp)"
after="$(mktemp)"
trap 'rm -f "$before" "$after"' EXIT

snapshot() {
  find web/templates internal/adapters/postgres/dbgen \
    web/assets/static/app.js \
    web/assets/static/login-background.js \
    web/assets/static/login-background-loader.js \
    -type f \( -name '*.go' -o -name '*.js' -o -name '*.css' -o -name '*.json' \) \
    -print0 | sort -z | xargs -0 sha256sum
}

snapshot >"$before"
"${MAKE:-make}" generate
snapshot >"$after"

if ! diff -u "$before" "$after"; then
  printf '%s\n' 'Generierte Dateien sind nicht aktuell. make generate ausführen und Änderungen committen.' >&2
  exit 1
fi

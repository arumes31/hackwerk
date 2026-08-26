#!/bin/sh
set -eu

output="$(mktemp -d)"
trap 'rm -rf "$output"' EXIT

check_asset() {
  name="$1"
  go tool minify --quiet --type js --output "$output/$name" "web/assets/src/$name"
  if ! cmp -s "$output/$name" "web/assets/static/$name"; then
    printf '%s\n' "web/assets/static/$name ist nicht aktuell; make assets ausführen." >&2
    exit 1
  fi
}

check_asset app.js
check_asset login-background.js
check_asset login-background-loader.js

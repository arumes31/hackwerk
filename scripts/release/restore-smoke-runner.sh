#!/bin/sh
set -eu
umask 077

: "${SOURCE_DATABASE_URL:?SOURCE_DATABASE_URL ist erforderlich}"
: "${RESTORE_ADMIN_DATABASE_URL:?RESTORE_ADMIN_DATABASE_URL ist erforderlich}"
: "${RESTORE_TEST_DATABASE_URL:?RESTORE_TEST_DATABASE_URL ist erforderlich}"

mkdir -p /work
printf '%s' "$SOURCE_DATABASE_URL" > /work/source-url
printf '%s' "$RESTORE_ADMIN_DATABASE_URL" > /work/admin-url
printf '%s' "$RESTORE_TEST_DATABASE_URL" > /work/restore-url

export SOURCE_DATABASE_URL_FILE=/work/source-url
export RESTORE_ADMIN_DATABASE_URL_FILE=/work/admin-url
export RESTORE_TEST_DATABASE_URL_FILE=/work/restore-url
export RESTORE_TEST_DIR=/work
exec /ops/restore-test.sh

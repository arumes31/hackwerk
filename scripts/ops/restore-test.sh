#!/bin/sh
set -eu

: "${SOURCE_DATABASE_URL_FILE:?SOURCE_DATABASE_URL_FILE ist erforderlich}"
: "${RESTORE_ADMIN_DATABASE_URL_FILE:?RESTORE_ADMIN_DATABASE_URL_FILE ist erforderlich}"
: "${RESTORE_TEST_DATABASE_URL_FILE:?RESTORE_TEST_DATABASE_URL_FILE ist erforderlich}"
: "${RESTORE_TEST_DATABASE:?RESTORE_TEST_DATABASE ist erforderlich}"
: "${RESTORE_TEST_DIR:?RESTORE_TEST_DIR ist erforderlich}"

case "$RESTORE_TEST_DATABASE" in hackwerk_restore_test_[a-z0-9_]*) ;; *) echo "Unsicherer Restore-Test-Datenbankname" >&2; exit 2;; esac
case "$RESTORE_TEST_DIR" in /*) ;; *) echo "RESTORE_TEST_DIR muss absolut sein" >&2; exit 2;; esac
mkdir -p -- "$RESTORE_TEST_DIR"
test_root=$(CDPATH= cd -- "$RESTORE_TEST_DIR" && pwd -P)
case "$test_root" in /|/bin|/boot|/dev|/etc|/home|/proc|/root|/run|/sys|/tmp|/usr|/var) echo "RESTORE_TEST_DIR ist zu breit" >&2; exit 2;; esac
work_dir=$(mktemp -d "$test_root/restore-test.XXXXXX")

admin_url=$(cat "$RESTORE_ADMIN_DATABASE_URL_FILE")
source_url=$(cat "$SOURCE_DATABASE_URL_FILE")
target_url=$(cat "$RESTORE_TEST_DATABASE_URL_FILE")
keep=${KEEP_RESTORE_TEST_DB:-false}
owner_role=${RESTORE_OWNER_ROLE:-hackwerk_migrate}
case "$owner_role" in *[!a-zA-Z0-9_]*) echo "Unsichere Restore-Owner-Rolle" >&2; exit 2;; esac

cleanup() {
  if [ "$keep" != true ]; then
    psql --dbname="$admin_url" --set=ON_ERROR_STOP=1 --set=target_db="$RESTORE_TEST_DATABASE" <<'SQL' >/dev/null
SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=:'target_db' AND pid <> pg_backend_pid();
SELECT format('DROP DATABASE IF EXISTS %I', :'target_db') \gexec
SQL
  fi
  case "$work_dir" in "$test_root"/restore-test.*) rm -rf -- "$work_dir";; *) echo "Unsicherer temporärer Pfad" >&2;; esac
}
trap cleanup EXIT HUP INT TERM

psql --dbname="$admin_url" --set=ON_ERROR_STOP=1 --set=target_db="$RESTORE_TEST_DATABASE" --set=owner_role="$owner_role" <<'SQL' >/dev/null
SELECT format('DROP DATABASE IF EXISTS %I', :'target_db') \gexec
SELECT format('CREATE DATABASE %I OWNER %I', :'target_db', :'owner_role')
WHERE EXISTS (SELECT FROM pg_roles WHERE rolname=:'owner_role') \gexec
SELECT format('CREATE DATABASE %I', :'target_db')
WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname=:'owner_role') \gexec
SQL

DATABASE_URL_FILE="$SOURCE_DATABASE_URL_FILE" BACKUP_DIR="$work_dir" "$(dirname "$0")/backup-postgres.sh" > "$work_dir/backup-path"
dump=$(cat "$work_dir/backup-path")
tampered="$work_dir/tampered.dump"
cp -- "$dump" "$tampered"
cp -- "$dump.sha256" "$tampered.sha256"
printf 'tampered' >> "$tampered"
if RESTORE_DATABASE_URL_FILE="$RESTORE_TEST_DATABASE_URL_FILE" "$(dirname "$0")/restore-postgres.sh" "$tampered" >/dev/null 2>&1; then
  echo "Manipulierter Dump wurde akzeptiert" >&2
  exit 1
fi
mkdir -p -- "$work_dir/offsite"
moved_dump="$work_dir/offsite/$(basename "$dump")"
mv -- "$dump" "$moved_dump"
mv -- "$dump.sha256" "$moved_dump.sha256"
RESTORE_DATABASE_URL_FILE="$RESTORE_TEST_DATABASE_URL_FILE" "$(dirname "$0")/restore-postgres.sh" "$moved_dump"

actual_database=$(psql --dbname="$target_url" --tuples-only --no-align --command="SELECT current_database()")
[ "$actual_database" = "$RESTORE_TEST_DATABASE" ] || { echo "Restore-Testziel stimmt nicht" >&2; exit 1; }
source_counts=$(psql --dbname="$source_url" --tuples-only --no-align --command="SELECT (SELECT count(*) FROM users)||':'||(SELECT count(*) FROM customers)||':'||(SELECT count(*) FROM jobs)||':'||(SELECT count(*) FROM appointments)||':'||(SELECT count(*) FROM audit_events)")
target_counts=$(psql --dbname="$target_url" --tuples-only --no-align --command="SELECT (SELECT count(*) FROM users)||':'||(SELECT count(*) FROM customers)||':'||(SELECT count(*) FROM jobs)||':'||(SELECT count(*) FROM appointments)||':'||(SELECT count(*) FROM audit_events)")
[ "$source_counts" = "$target_counts" ] || { echo "Restore-Integritätszählung stimmt nicht" >&2; exit 1; }

runtime_roles=$(psql --dbname="$target_url" --tuples-only --no-align --command="SELECT (EXISTS (SELECT FROM pg_roles WHERE rolname='hackwerk_app') AND EXISTS (SELECT FROM pg_roles WHERE rolname='hackwerk_migrate'))::text")
if [ "$runtime_roles" = true ]; then
  runtime_privileges=$(psql --dbname="$target_url" --tuples-only --no-align --command="SELECT has_table_privilege('hackwerk_app','customers','SELECT')||':'||has_table_privilege('hackwerk_app','customers','INSERT')||':'||has_table_privilege('hackwerk_app','jobs','UPDATE')||':'||has_table_privilege('hackwerk_app','availability_rules','DELETE')||':'||has_table_privilege('hackwerk_app','audit_events','INSERT')||':'||has_table_privilege('hackwerk_app','audit_events','UPDATE')||':'||has_table_privilege('hackwerk_app','audit_events','DELETE')||':'||has_table_privilege('hackwerk_app','schema_metadata','UPDATE')||':'||has_table_privilege('hackwerk_app','goose_db_version','DELETE')")
  [ "$runtime_privileges" = "true:true:true:true:true:false:false:false:false" ] || { echo "Restore-Rollenintegrität stimmt nicht" >&2; exit 1; }
  owner_mismatches=$(psql --dbname="$target_url" --tuples-only --no-align --command="SELECT (SELECT count(*) FROM pg_tables WHERE schemaname='public' AND tableowner<>'hackwerk_migrate') + (SELECT count(*) FROM pg_sequences WHERE schemaname='public' AND sequenceowner<>'hackwerk_migrate')")
  [ "$owner_mismatches" = 0 ] || { echo "Restore-Ownership stimmt nicht" >&2; exit 1; }
fi
printf 'Restore-Test erfolgreich; geprüfte Tabellenzählungen stimmen überein.\n'

#!/bin/sh
set -eu

: "${RESTORE_DATABASE_URL_FILE:?RESTORE_DATABASE_URL_FILE ist erforderlich}"
: "${1:?Pfad zum Dump ist erforderlich}"
schema_binary=${HACKWERK_BINARY:-hackwerk}
if ! command -v "$schema_binary" >/dev/null 2>&1; then
  echo "HackWerk-Binary für die kanonische Schemaversion fehlt" >&2
  exit 2
fi
expected_schema_version=$("$schema_binary" schema-version)
case "$expected_schema_version" in
  *[!0-9]*|'') echo "HackWerk-Binary lieferte keine gültige Schemaversion" >&2; exit 2;;
esac
dump=$1
[ -f "$dump" ] || { echo "Dump existiert nicht" >&2; exit 2; }
[ -f "$dump.sha256" ] || { echo "Prüfsummendatei fehlt" >&2; exit 2; }
expected_checksum=$(awk 'NR == 1 { print $1 }' "$dump.sha256")
case "$expected_checksum" in
  *[!0-9a-fA-F]*|'') echo "Ungültige Prüfsummendatei" >&2; exit 2;;
esac
[ "${#expected_checksum}" -eq 64 ] || { echo "Ungültige Prüfsummendatei" >&2; exit 2; }
actual_checksum=$(sha256sum "$dump" | awk '{print $1}')
[ "$actual_checksum" = "$expected_checksum" ] || { echo "Dump-Prüfsumme stimmt nicht" >&2; exit 1; }

database_url=$(cat "$RESTORE_DATABASE_URL_FILE")
table_count=$(psql --dbname="$database_url" --tuples-only --no-align --command="SELECT count(*) FROM pg_tables WHERE schemaname='public'")
[ "$table_count" = 0 ] || { echo "Restore-Zieldatenbank ist nicht leer" >&2; exit 2; }

restore_role=$(psql --dbname="$database_url" --tuples-only --no-align --command="SELECT CASE WHEN EXISTS (SELECT FROM pg_roles WHERE rolname='hackwerk_migrate') THEN 'hackwerk_migrate' ELSE '' END")
if [ -n "$restore_role" ]; then
  pg_restore --dbname="$database_url" --exit-on-error --no-owner --no-acl --role="$restore_role" "$dump"
else
  pg_restore --dbname="$database_url" --exit-on-error --no-owner --no-acl "$dump"
fi

# A no-owner/no-ACL restore intentionally discards role grants. Reapply the
# runtime role's least privileges when the production roles exist in this
# cluster, and verify the audit log remains append-only for the application.
runtime_roles=$(psql --dbname="$database_url" --tuples-only --no-align --command="SELECT (EXISTS (SELECT FROM pg_roles WHERE rolname='hackwerk_app') AND EXISTS (SELECT FROM pg_roles WHERE rolname='hackwerk_migrate'))::text")
if [ "$runtime_roles" = true ]; then
  psql --dbname="$database_url" --set=ON_ERROR_STOP=1 <<'SQL'
GRANT USAGE ON SCHEMA public TO hackwerk_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO hackwerk_app;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO hackwerk_app;
REVOKE UPDATE, DELETE ON TABLE audit_events FROM hackwerk_app;
REVOKE INSERT, UPDATE, DELETE ON TABLE schema_metadata, goose_db_version FROM hackwerk_app;
ALTER DEFAULT PRIVILEGES FOR ROLE hackwerk_migrate IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO hackwerk_app;
ALTER DEFAULT PRIVILEGES FOR ROLE hackwerk_migrate IN SCHEMA public GRANT USAGE, SELECT ON SEQUENCES TO hackwerk_app;
SQL
  runtime_privileges=$(psql --dbname="$database_url" --tuples-only --no-align --command="SELECT has_table_privilege('hackwerk_app','customers','SELECT')||':'||has_table_privilege('hackwerk_app','customers','INSERT')||':'||has_table_privilege('hackwerk_app','jobs','UPDATE')||':'||has_table_privilege('hackwerk_app','availability_rules','DELETE')||':'||has_table_privilege('hackwerk_app','audit_events','INSERT')||':'||has_table_privilege('hackwerk_app','audit_events','UPDATE')||':'||has_table_privilege('hackwerk_app','audit_events','DELETE')||':'||has_table_privilege('hackwerk_app','schema_metadata','UPDATE')||':'||has_table_privilege('hackwerk_app','goose_db_version','DELETE')")
  [ "$runtime_privileges" = "true:true:true:true:true:false:false:false:false" ] || { echo "Restore-Rollenprüfung fehlgeschlagen" >&2; exit 1; }
  owner_mismatches=$(psql --dbname="$database_url" --tuples-only --no-align --command="SELECT (SELECT count(*) FROM pg_tables WHERE schemaname='public' AND tableowner<>'hackwerk_migrate') + (SELECT count(*) FROM pg_sequences WHERE schemaname='public' AND sequenceowner<>'hackwerk_migrate')")
  [ "$owner_mismatches" = 0 ] || { echo "Restore-Objekte gehören nicht der Migrationsrolle" >&2; exit 1; }
fi

application=$(psql --dbname="$database_url" --tuples-only --no-align --command="SELECT value FROM schema_metadata WHERE key='application'")
schema_version=$(psql --dbname="$database_url" --tuples-only --no-align --command="SELECT value FROM schema_metadata WHERE key='application_schema_version'")
[ "$application" = hackplan ] && [ "$schema_version" = "$expected_schema_version" ] || { echo "Restore-Schemaprüfung fehlgeschlagen" >&2; exit 1; }
printf 'Restore erfolgreich; Schema %s.\n' "$schema_version"

#!/bin/sh
set -eu
umask 077

migration_password=$(cat "$HACKWERK_MIGRATION_PASSWORD_FILE")
runtime_password=$(cat "$HACKWERK_RUNTIME_PASSWORD_FILE")
export migration_password runtime_password

psql --set=ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<'SQL'
\getenv migration_password migration_password
\getenv runtime_password runtime_password
SELECT format('CREATE ROLE hackwerk_migrate LOGIN PASSWORD %L', :'migration_password')
WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname='hackwerk_migrate') \gexec
SELECT format('CREATE ROLE hackwerk_app LOGIN PASSWORD %L', :'runtime_password')
WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname='hackwerk_app') \gexec

CREATE EXTENSION IF NOT EXISTS citext;
CREATE EXTENSION IF NOT EXISTS btree_gist;
CREATE EXTENSION IF NOT EXISTS pgcrypto;
GRANT CONNECT ON DATABASE hackwerk TO hackwerk_migrate, hackwerk_app;
GRANT USAGE, CREATE ON SCHEMA public TO hackwerk_migrate;
GRANT USAGE ON SCHEMA public TO hackwerk_app;
ALTER DEFAULT PRIVILEGES FOR ROLE hackwerk_migrate IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO hackwerk_app;
ALTER DEFAULT PRIVILEGES FOR ROLE hackwerk_migrate IN SCHEMA public GRANT USAGE, SELECT ON SEQUENCES TO hackwerk_app;
SQL

unset migration_password runtime_password

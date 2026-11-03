# shellcheck shell=sh
# Shared libpq credential handling for the PostgreSQL ops scripts.
#
# Connection strings are read from secret files and must never reach the
# argv of psql/pg_dump/pg_restore, where any local user can read them via
# /proc or ps. pg_connection_prepare strips the password from a
# postgres:// or postgresql:// URL, writes it into a private libpq passfile
# and pg_run passes only the password-free URL on the command line with
# PGPASSFILE pointing at that file. Key=value conninfo strings are passed
# through unchanged only when they carry no password keyword.
#
# Callers must run pg_credentials_cleanup from their EXIT trap.

pg_credentials_dir=

pg_credentials_fail() {
  echo "$1" >&2
  exit 2
}

pg_credentials_cleanup() {
  if [ -n "$pg_credentials_dir" ]; then
    case "$pg_credentials_dir" in
      */pg-credentials.*) rm -rf -- "$pg_credentials_dir" ;;
    esac
    pg_credentials_dir=
  fi
}

# pg_percent_decode reads one percent-encoded line on stdin and prints it as a
# printf %b argument: every %XX becomes an octal escape and literal
# backslashes are doubled, so only the escapes produced here are interpreted.
pg_percent_decode() {
  awk 'BEGIN { hex = "0123456789abcdef"; status = 0 }
  {
    out = ""
    n = length($0)
    for (i = 1; i <= n; i++) {
      c = substr($0, i, 1)
      if (c == "%") {
        hi = index(hex, tolower(substr($0, i + 1, 1)))
        lo = index(hex, tolower(substr($0, i + 2, 1)))
        if (i + 2 > n || hi == 0 || lo == 0) { status = 1; exit }
        out = out sprintf("\\0%03o", (hi - 1) * 16 + lo - 1)
        i += 2
        continue
      }
      if (c == "\\") c = "\\\\"
      out = out c
    }
    printf "%s", out
  }
  END { exit status }'
}

# pg_passfile_escape escapes the characters libpq treats specially in a
# passfile field.
pg_passfile_escape() {
  sed -e 's/\\/\\\\/g' -e 's/:/\\:/g'
}

# pg_connection_prepare NAME CONNECTION_STRING
# Sets pg_dsn_NAME to a password-free connection string and, when the input
# carried a password, writes it to a per-connection passfile.
pg_connection_prepare() {
  pgc_name=$1
  pgc_connection=$2
  case "$pgc_name" in *[!a-z_]*|'') pg_credentials_fail "Ungültiger Verbindungsname" ;; esac
  [ -n "$pgc_connection" ] || pg_credentials_fail "Datenbank-URL ist leer"
  case "$pgc_connection" in
    *"
"*) pg_credentials_fail "Datenbank-URL darf keinen Zeilenumbruch enthalten" ;;
  esac

  pgc_password=
  pgc_has_password=false
  case "$pgc_connection" in
    postgres://*|postgresql://*)
      pgc_scheme=${pgc_connection%%://*}
      pgc_rest=${pgc_connection#*://}
      # Like libpq, user info ends at the first '@' before any '/'.
      pgc_userinfo=
      pgc_authority=${pgc_rest%%/*}
      case "$pgc_authority" in *@*) pgc_userinfo=${pgc_authority%%@*}; pgc_rest=${pgc_rest#*@} ;; esac
      pgc_query=
      case "$pgc_rest" in *\?*) pgc_query=${pgc_rest#*\?}; pgc_rest=${pgc_rest%%\?*} ;; esac
      pgc_path=
      case "$pgc_rest" in */*) pgc_path=/${pgc_rest#*/}; pgc_rest=${pgc_rest%%/*} ;; esac
      pgc_authority=$pgc_rest
      pgc_user=$pgc_userinfo
      case "$pgc_userinfo" in
        *:*) pgc_user=${pgc_userinfo%%:*}; pgc_password=${pgc_userinfo#*:}; pgc_has_password=true ;;
      esac

      pgc_kept=
      pgc_remaining=$pgc_query
      while [ -n "$pgc_query" ]; do
        case "$pgc_remaining" in
          *\&*) pgc_item=${pgc_remaining%%&*}; pgc_remaining=${pgc_remaining#*&} ;;
          *) pgc_item=$pgc_remaining; pgc_remaining= ;;
        esac
        pgc_key=${pgc_item%%=*}
        pgc_decoded_key=$(printf '%s\n' "$pgc_key" | pg_percent_decode) || pg_credentials_fail "Ungültige Prozentkodierung in der Datenbank-URL"
        pgc_decoded_key=$(printf '%b' "$pgc_decoded_key")
        if [ "$pgc_decoded_key" = password ]; then
          pgc_password=${pgc_item#*=}
          pgc_has_password=true
        elif [ -n "$pgc_item" ]; then
          pgc_kept=${pgc_kept:+$pgc_kept&}$pgc_item
        fi
        [ -n "$pgc_remaining" ] || break
      done

      pgc_dsn="$pgc_scheme://"
      [ -z "$pgc_user" ] || pgc_dsn="$pgc_dsn$pgc_user@"
      pgc_dsn="$pgc_dsn$pgc_authority$pgc_path"
      [ -z "$pgc_kept" ] || pgc_dsn="$pgc_dsn?$pgc_kept"
      ;;
    *://*)
      pg_credentials_fail "Nicht unterstütztes Datenbank-URL-Schema; erwartet postgres:// oder postgresql://"
      ;;
    *)
      if printf '%s\n' "$pgc_connection" | grep -Eqi '(^|[[:space:]])password[[:space:]]*='; then
        pg_credentials_fail "Key=Value-Verbindungen mit Passwort werden nicht unterstützt; postgres://-URL oder PGPASSFILE verwenden"
      fi
      pgc_dsn=$pgc_connection
      ;;
  esac

  if [ "$pgc_has_password" = true ]; then
    case "$pgc_password" in
      *%00*|*%0[aAdD]*) pg_credentials_fail "Datenbankpasswort enthält unzulässige Steuerzeichen" ;;
    esac
    pgc_encoded=$(printf '%s\n' "$pgc_password" | pg_percent_decode) || pg_credentials_fail "Ungültige Prozentkodierung im Datenbankpasswort"
    if [ -z "$pg_credentials_dir" ]; then
      pg_credentials_dir=$(umask 077 && mktemp -d "${TMPDIR:-/tmp}/pg-credentials.XXXXXX") || pg_credentials_fail "Temporäres Credential-Verzeichnis konnte nicht angelegt werden"
    fi
    pgc_passfile="$pg_credentials_dir/$pgc_name.pass"
    (
      umask 077
      pgc_escaped=$(printf '%b' "$pgc_encoded" | pg_passfile_escape)
      printf '*:*:*:*:%s\n' "$pgc_escaped" > "$pgc_passfile"
    ) || pg_credentials_fail "Passfile konnte nicht geschrieben werden"
    chmod 600 "$pgc_passfile"
  fi
  eval "pg_dsn_$pgc_name=\$pgc_dsn"
  unset pgc_password pgc_encoded pgc_userinfo pgc_connection
}

# pg_run NAME COMMAND [ARGS...] runs a libpq client with --dbname set to the
# password-free connection string and PGPASSFILE set when one was written.
pg_run() {
  pgr_name=$1
  pgr_command=$2
  shift 2
  case "$pgr_name" in *[!a-z_]*|'') pg_credentials_fail "Ungültiger Verbindungsname" ;; esac
  eval "pgr_dsn=\${pg_dsn_$pgr_name?Verbindung $pgr_name ist nicht vorbereitet}"
  if [ -n "$pg_credentials_dir" ] && [ -f "$pg_credentials_dir/$pgr_name.pass" ]; then
    PGPASSFILE="$pg_credentials_dir/$pgr_name.pass" "$pgr_command" --dbname="$pgr_dsn" "$@"
  else
    "$pgr_command" --dbname="$pgr_dsn" "$@"
  fi
}

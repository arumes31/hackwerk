# Betrieb und Deployment

## Zieltopologie

```text
Internet/VPN
  -> Reverse Proxy mit TLS
  -> app container
  -> PostgreSQL im privaten Netz
  -> worker container
  -> externe SMTP/SMS/Speech/Route Provider
```

App und Worker verwenden dasselbe unveränderliche Image und unterschiedliche Subcommands.

## Docker-Image

- Builder auf `golang:1.27.x`;
- kein Node-/npm-/esbuild-Stage; statische Browser-Assets werden vom Go-Compiler eingebettet;
- FullCalendar Standard 7.0.2 und Classic-Theme sind als geprüfte MIT-Assets lokal unter `web/assets/static` gebündelt; Prüfsummen und Quellen stehen in `web/assets/FULLCALENDAR.md`, es gibt keine Laufzeit-CDN-Verbindung;
- `templ`, `sqlc` und Migrationstool als im Modul fixierte Go Tools;
- statisches oder minimal dynamisches Binary;
- non-root Runtime;
- CA-Zertifikate und Zeitzonendaten;
- kein Shellbedarf im Runtime-Image;
- `/tmp` als begrenztes `tmpfs` für Audio;
- OCI Labels mit Version, Commit und Buildzeit;
- Healthcheck über `hackwerk healthcheck`.

## Compose Development

Services:

- `postgres` mit named volume;
- kein lokaler Maildienst und kein E-Mail-Empfang; SMTP-Ziel und Zugangsdaten für den Versand kommen ausschließlich aus Deployment-Konfiguration/Secrets;
- `app` mit automatischer Migration nur im Development-Profil;
- `worker`;
- optional `assets`/watch mode.

Development-Secrets sind eindeutig als nicht produktiv markiert. Das Compose-File publiziert PostgreSQL nicht standardmäßig ins externe Netz.

## Produktion

- Migration als expliziter Deployment-Schritt `hackwerk migrate up`;
- App erst starten, wenn erwartete Schema-Version vorhanden ist;
- mindestens zwei getrennte DB-Rollen: Migration und Runtime;
- Runtime-Rolle darf Audit nicht löschen und Schema nicht ändern;
- Reverse Proxy setzt Request-ID, TLS und begrenzte Bodygrößen;
- App vertraut Forwarded Header nur aus konfigurierten Proxy-Netzen;
- Read-only Root-Filesystem, `no-new-privileges`, Capability Drop;
- Ressourcenlimits und Restart Policy;
- Providerzugriffe mit Egress-Regeln, soweit Umgebung das erlaubt.

## Healthchecks

- Liveness: Prozess antwortet, keine Providerabhängigkeit;
- Readiness: DB erreichbar, Migration kompatibel, notwendige Konfiguration geladen;
- Worker readiness: DB und Outbox-Claim möglich;
- Providerausfall macht App nicht unready, wird aber als degraded Metrik gezeigt.

## Logs und Metriken

### Logs

JSON nach stdout mit:

- timestamp, level, service, version;
- request_id, route_template, status, duration;
- actor_id falls angemeldet;
- fachlicher Fehlercode;
- keine PII/Tokens.

### Metriken

- HTTP Requests/Latenz/Fehler;
- aktive Sessions grob;
- DB-Pool;
- Outbox queue age/count;
- Notification sent/retry/failed;
- Planning run duration/provider fallback;
- Voice requests/error/duration ohne Audioinhalt;
- Confirmation responses;
- Backup-Erfolg wird außerhalb App überwacht.

## Backup

- täglicher konsistenter PostgreSQL-Dump oder Infrastruktur-Snapshot;
- verschlüsselte Speicherung;
- definierte Aufbewahrung;
- Restore-Test mindestens regelmäßig und vor großen Upgrades;
- Sicherung umfasst DB, notwendige Konfiguration/Secrets separat, aber keine vergänglichen Audiofiles;
- Runbook mit Restore in neue Instanz und Integritätsprüfung.

## Upgrade

1. Backup und Restore-Verifikation;
2. Release Notes/Migrationsplan prüfen;
3. Image bauen/scannen/signieren;
4. Migration ausführen;
5. App/Worker rollen;
6. Readiness und Smoke-Test;
7. Outbox/Notification-Metriken beobachten;
8. Rollbackstrategie abhängig von Migration dokumentieren.

## Aufräumjobs

Worker führt idempotent aus:

- abgelaufene Sessions;
- abgelaufene Confirmation Tokens;
- alte Voice Drafts/temporäre Dateien;
- verarbeitete Outbox-Payloads nach Retention;
- optional alte Rate-Limit-Zähler.

## Admin-CLI

Mindestens:

```text
hackwerk admin create --username ...
hackwerk admin reset-password --username ...
hackwerk admin disable-user --username ...
hackwerk calendar-feed revoke --id ...
hackwerk healthcheck
hackwerk migrate up
hackwerk seed-dev
```

Passwörter nicht als Shellargument erzwingen; bevorzugt sicherer Prompt/stdin.

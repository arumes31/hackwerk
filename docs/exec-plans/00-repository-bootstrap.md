# ExecPlan: Task 00 – Repository, Toolchain und Docker-Fundament

## Ziel und sichtbares Ergebnis

Ein reproduzierbares Go-1.27-Projekt liefert ein Binary mit den geforderten Subcommands, eine deutschsprachige responsive Basisseite, getrennte Live-/Ready-Healthchecks, eingebettete Migrationen/Assets sowie App-, Worker- und PostgreSQL-Container. CI und lokale Befehle verwenden dieselben Gates; E-Mail-Versand wird später ausschließlich über externes SMTP angebunden, E-Mail-Empfang ist nicht vorgesehen.

## Kontext und betroffene Bereiche

- Task: `codex/tasks/00-repository-bootstrap.md`
- Architektur/Security/Test/Betrieb: `docs/02-architecture.md`, `docs/10-security-privacy.md`, `docs/11-test-strategy.md`, `docs/12-operations-deployment.md`, `docs/14-configuration.md`
- Struktur/Config: `reference/repository-tree.txt`, `reference/environment.example`
- Neue Bereiche: `cmd/hackwerk`, `internal/app`, `internal/config`, `internal/adapters/postgres`, `internal/web`, `db`, `web`, `scripts`, `.github/workflows`, Docker-/Make-Artefakte

## Annahmen und feste Entscheidungen

- Modulpfad vorläufig `example.invalid/hackplan`, da kein Git-Remote existiert.
- Go 1.27.0 und PostgreSQL 18.6; Runtime-/Toolabhängigkeiten werden konkret gepinnt.
- Goose-Migrationen werden in das Binary eingebettet. sqlc-/templ-Werkzeuge werden über das Go-Modul fixiert.
- Responsive Basisshell: ruhig-industriell mit Holz/Moos/Ocker, lokal gebündelte Assets, keine Webfonts/CDNs; klare Typografie, 44-px-Ziele und reduzierte Bewegung.
- Auf ausdrückliche Benutzervorgabe gibt es kein Node und kein npm. Eigenes CSS/JavaScript sowie erforderliche, prüfsummengesicherte Browserbibliotheken werden ohne Frontend-Build direkt eingebettet.
- Task 00 enthält bewusst keine fachliche CRUD-/Authlogik. `worker`, `seed-dev` und `admin` besitzen ehrliche, dokumentierte Basissemanik ohne vorgetäuschte Fachfunktion.

## Risiken

- Tooling ohne lokales GNU Make muss dennoch auf Unix/CI funktionieren; direkte Go-Befehle dienen lokal als Äquivalentnachweis.
- Readiness darf PostgreSQL prüfen, aber niemals Migrationen ausführen.
- Generierung muss auf Windows und im Linux-Container reproduzierbar sein.
- Docker ist mit expliziter Sandbox-Freigabe erreichbar; PostgreSQL 18 benötigt den Volume-Mount auf `/var/lib/postgresql`.
- Keine Konfiguration oder Fehlermeldung darf den Connection String ausgeben.

## Umsetzungsschritte

1. Modulpfad/Versionen und Werkzeugpins festlegen; Repositorystruktur und Ignore-Dateien anlegen.
2. Typisierte Environmentkonfiguration mit Developmentdefaults, Production-Fail-closed und Tests implementieren.
3. pgx-Pool, eingebettete Goose-Migrationen und Migration-/Ready-Operationen implementieren.
4. CLI-Dispatcher mit `serve`, `worker`, `migrate`, `seed-dev`, `admin`, `healthcheck`, `version` und stabilen Exitcodes bauen.
5. chi-HTTP-Server, Request-ID/Logging/Recovery/Timeouts, Healthrouten und zugängliche Basisshell implementieren.
6. templ-/sqlc-Generierung und direkt eingebettete statische CSS-/JavaScript-Assets reproduzierbar einrichten; externe Browserbibliotheken nur vendort und mit Prüfsumme.
7. Dockerfile, Compose, Productionreferenz, Healthchecks und non-root/read-only-fähige Runtime erstellen.
8. Make-/CI-Gates, Unit-/Handler-/Integrationstests, OpenAPI-Basis und Quickstart dokumentieren.
9. Generierung, Format, Lint, Unit, Integration, Build, Compose-Konfiguration und Container-Smoke ausführen; Diff gegen Taskkriterien prüfen.

## Datenbankänderungen

- Migration `00001_bootstrap`: begründete Extensions `citext`, `btree_gist`, `pgcrypto` sowie Schema-Metadatenbasis.
- Up/Down werden eingebettet und über Goose mit PostgreSQL-Advisory-/Migrationlock ausgeführt.
- Integrationstests nutzen eine explizite isolierte Test-URL und führen Migrationen selbst aus.
- Runtime-Readiness liest nur Migrationsstatus/DB-Verfügbarkeit und mutiert nichts.

## Testplan

- Unit: Configdefaults, Typ-/Bereichsvalidierung, Production-Fail-closed, CLI-Argumente und Redaction.
- HTTP: Basisseite, 404, Live ohne DB, Ready mit/ohne DB, Request-ID und Securitybaseline.
- Integration: Migration up/idempotent/status/down/up auf PostgreSQL 18.
- Build/Assets: templ/sqlc deterministisch, `generate-check`, direkt eingebettete lokale Assets ohne Node, npm oder externe URL.
- Docker: Compose-Parsing, Services/Health, non-root, Signalhandling, DB-Ausfall und HTML-/Health-Smoke.

## Fortschritt

- [x] Ausgangslage, Versionen, Tooling und Spezifikation auditiert
- [x] Struktur, Module und Werkzeugpins
- [x] Konfiguration und Datenbankbasis
- [x] CLI und HTTP/Health
- [x] Templates und direkt eingebettete Assets
- [x] Docker/Compose und CI ohne Node/npm
- [x] Container-Smoke und Task-Selbstreview

## Entdeckungen und Entscheidungen während der Umsetzung

- 2026-08-25: `go.mod` war ein unversioniertes Zusatzartefakt ohne Git-Herkunft; seine veralteten Abhängigkeiten werden gezielt aktualisiert, nicht als Produktspezifikation behandelt.
- 2026-08-25: Lokales Go ist exakt 1.27.0. PostgreSQL 18.6 ist der aktuelle 18.x-Patchstand.
- 2026-08-25: Node, npm, Lockfile, Frontend-Buildskripte und `node_modules` wurden auf Benutzervorgabe entfernt. Statische Browser-Assets werden direkt durch Go eingebettet; spätere Fremdbibliotheken benötigen eine feste Version und dokumentierte Prüfsumme.
- 2026-08-25: Produktname und ausführbares Artefakt heißen nun `HackWerk` beziehungsweise `hackwerk`; der interne Go-Modulpfad und persistente Datenbank-/Cookie-Bezeichner bleiben kompatibel.
- 2026-08-25: Der erste Container-Smoke deckte den mit PostgreSQL 18 inkompatiblen Mount `/var/lib/postgresql/data` auf. Compose verwendet nun den versionsgerechten Parent-Mount `/var/lib/postgresql`; der Neustartnachweis ist noch offen.
- 2026-08-25: Ein rund 3 GB großer lokaler `.cache`-Ordner blockierte die Übertragung des Docker-Buildkontexts. `.dockerignore` schließt ihn nun ausdrücklich aus; Cache-Inhalte werden weder ins Image kopiert noch für den Build übertragen.
- 2026-08-25: Abschluss-Smoke erfolgreich: Node-freier Scratch-Build, PostgreSQL 18.6 gesund, Migrationen aktuell, App gesund und Worker aktiv. App/Worker verwenden UID/GID 65532, read-only Rootfs, `cap_drop: ALL` und `no-new-privileges`; `/health/live`, `/health/ready` und `/hackwerk version` wurden im Compose-Netz geprüft. Der später entfernte lokale Maildienst ist kein Bestandteil der Zielarchitektur.
- 2026-08-25: Auf Benutzervorgabe verwendet HackWerk innen wie außen Port `18533`; der Compose-HTTP-Smoke prüft genau diese Zuordnung.

## Abschlussnachweis

Nachweis: `go test ./...` und der Windows-Build `go build -o bin\\hackwerk.exe .\\cmd\\hackwerk` sind erfolgreich. Docker überträgt einen Buildkontext von rund 8–16 kB, baut `/hackwerk` mit Go 1.27.0 in eine Scratch-Runtime und startet die Compose-Dienste erfolgreich. `migrate up` wendet 00001–00003 an und ist idempotent; Live/Ready liefern 200. Der Namens-/Toolingaudit findet keine aktive Node-/npm-Projektabhängigkeit und keine veralteten sichtbaren CLI-Pfade.

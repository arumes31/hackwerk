# Task 00 – Repository, Toolchain und lauffähiges Docker-Fundament

**Empfohlener Aufruf**

```text
$hackplan-implement Implementiere codex/tasks/00-repository-bootstrap.md vollständig.
```

## Ziel

Aus dem Prompt-Paket entsteht ein reproduzierbares, lokal startbares Go-Projekt. `docker compose up --build` startet PostgreSQL, Webdienst, Worker und eine lokale Mail-Testoberfläche. Die App zeigt eine deutschsprachige Basisseite und liefert funktionierende Healthchecks. Noch keine fachlichen CRUD-Module.

## Vor der Implementierung lesen

- `AGENTS.md`, `PLANS.md`
- `docs/02-architecture.md`
- `docs/10-security-privacy.md`
- `docs/11-test-strategy.md`
- `docs/12-operations-deployment.md`
- `docs/14-configuration.md`
- `reference/repository-tree.txt`
- `reference/environment.example`

Erstelle `docs/exec-plans/00-repository-bootstrap.md`.

## Entscheidungen, die in diesem Task getroffen werden

- Leite den Go-Modulpfad aus einem plausiblen Git-Remote ab. Fehlt eines, verwende vorübergehend `example.invalid/hackplan`, dokumentiere die Umstellung und verteile den String nicht außerhalb von `go.mod`/Imports.
- Pinne Go auf die aktuelle `1.27.x`-Patchversion, PostgreSQL auf `18.x` und direkte Abhängigkeiten auf konkrete Versionen.
- Nutze `go tool`-basierte oder anderweitig im Modul fixierte Generator-/Migrationstools; keine unversionierten `@latest`-Downloads in CI.

## Scope

### Repository und Go-Binary

- Initialisiere `go.mod`, `go.sum` und die Struktur aus `reference/repository-tree.txt`.
- Erzeuge `cmd/hackwerk/main.go` und ein Binary mit Subcommands:
  - `serve`
  - `worker`
  - `migrate up|down|status`
  - `seed-dev`
  - `admin` als zunächst dokumentierter Platzhalter mit sauberem Exitcode
  - `healthcheck`
- Implementiere Konfigurationsladen aus Environment mit Typprüfung, sicheren Defaults für Entwicklung und Fail-fast bei ungültigen produktionsrelevanten Werten.
- Implementiere kontrolliertes Shutdown bei SIGTERM/SIGINT, HTTP-Timeouts und strukturierte `slog`-Logs mit Request-ID.
- Erzeuge `/health/live` und `/health/ready`; Readiness prüft PostgreSQL mit kurzem Timeout.

### Datenbank und Codegenerierung

- Richte PostgreSQL-Verbindungspool via `pgx/v5` ein.
- Lege eine erste Migration für notwendige Extensions und eine Schema-Metadatenbasis an. Aktiviere nur begründete Extensions, insbesondere `citext`, `btree_gist` und gegebenenfalls `pgcrypto`.
- Richte `sqlc` so ein, dass Queries reproduzierbar generiert werden.
- Nutze getrennte DB-Rollen/URLs, soweit für lokale Entwicklung sinnvoll dokumentierbar; die App darf nicht stillschweigend als PostgreSQL-Superuser betrieben werden.
- Integrationstests erhalten eine isolierte Testdatenbank und führen Migrationen automatisch aus.

### Web- und Asset-Fundament

- Richte `chi/v5`, zentrale Middleware, Fehlerseiten und `templ` ein.
- Baue eine responsive Shell mit Skip-Link, Header, leerem Navigationsrahmen, Hauptinhalt und Footer; UI-Sprache Deutsch.
- Richte htmx und TypeScript/esbuild als lokal gebündelte Assets ein. Keine Runtime-CDNs.
- FullCalendar-Pakete dürfen bereits installiert und im Asset-Lockfile gepinnt werden, werden aber erst später fachlich verwendet.
- Generierte Assets und templ-Ausgaben müssen reproduzierbar sein; entscheide explizit, welche Artefakte committed werden.
- Erzeuge eine unautorisierte Basisseite mit App-Name und Build-Version sowie verständliche 404/500-Seiten ohne interne Details.

### Docker und lokale Entwicklung

- Multi-stage `Dockerfile` mit separaten Stages für Assetbuild, Go-Build und schlankes Runtime-Image.
- Runtime als non-root, kein Compiler/Package Manager im finalen Image, `tini` oder korrektes PID-1-Signalhandling.
- `compose.yaml` mindestens mit:
  - `postgres`
  - `app` (`serve`)
  - `worker`
  - kein lokaler Maildienst und kein E-Mail-Empfang; späterer Versand verwendet externe SMTP-Konfiguration und einen Fake-SMTP-Adapter in automatisierten Tests
- Healthchecks und `depends_on` nur ergänzend; der Go-Dienst muss DB-Startverzögerungen sauber behandeln.
- `.dockerignore`, `.gitignore`, `.env.example` aus der Referenz, benannte Volumes und internes Netzwerk.
- Keine echten Secrets in Compose oder Repository.

### Entwicklerwerkzeuge und CI

- `Makefile` oder gleichwertige stabile Befehle für `dev`, `up`, `down`, `logs`, `generate`, `generate-check`, `format`, `lint`, `test`, `test-integration`, `test-e2e`, `test-race`, `build`, `check`, `scan`, `release-check`.
- Pinne einen Go-Linter und führe `go vet` aus. Konfiguration darf nicht tausende irrelevante Regeln aktivieren.
- GitHub Actions oder generische CI-Konfiguration für Build, Generatorcheck, Lint, Unit und PostgreSQL-Integrationstests.
- Buildmetadaten (`version`, `commit`, `build time`) via ldflags, sichtbar in `hackwerk version` und optional als sicherer Teil der Healthantwort.
- `README.md` um Quickstart, Ports, Befehle und Fehlerbehebung ergänzen; den Prompt-Paket-Überblick nicht verlieren.

## Verbindliche Regeln

- Keine globale mutable Geschäftslogik.
- Kein Redis, keine zweite persistente Datenbank.
- Keine geheimen Daten, Connection Strings oder Environment-Dumps in Logs.
- Readiness darf keine Migrationen ausführen.
- `serve` und `worker` nutzen dieselbe Konfigurations-/DB-Basis, aber getrennte Prozesse.
- Container werden nicht als root ausgeführt.
- Keine Onlineabhängigkeit im laufenden Browser außer später konfigurierten Integrationen.

## Nicht Bestandteil

- Login, Rollen und Benutzerverwaltung;
- fachliche Kunden-/Termin-/Wartelistenfunktionen;
- produktive Benachrichtigungs- oder Sprachprovider;
- Kubernetes-Manifeste.

## Akzeptanzkriterien

- [ ] `docker compose up --build` startet alle lokalen Services erfolgreich.
- [ ] Die Basisseite lädt ohne CDN und ist bei 360 px sowie Desktopbreite nutzbar.
- [ ] `/health/live` antwortet ohne DB, `/health/ready` schlägt bei fehlender DB korrekt fehl.
- [ ] Migrationen können wiederholt sicher auf eine frische Datenbank angewendet werden.
- [ ] `serve`, `worker`, `migrate`, `seed-dev`, `admin`, `healthcheck`, `version` besitzen klare Help-/Exitcode-Semantik.
- [ ] `make generate-check`, `make lint`, `make test`, `make test-integration` und `make build` laufen erfolgreich.
- [ ] Das Runtime-Image läuft non-root und enthält keine Build-Secrets.
- [ ] Logs enthalten Request-ID, Level und Nachricht, aber keine Environment-Dumps.
- [ ] CI nutzt dieselben zentralen Befehle wie lokal.
- [ ] ExecPlan und Quickstart dokumentieren die real implementierten Befehle.

## Pflichtprüfungen

```bash
make generate
make generate-check
make format
make lint
make test
make test-integration
make build
make check
docker compose config
docker compose up -d --build
# danach Health- und HTML-Smoke-Check sowie docker compose logs ohne Panics
```

## Abschlussbericht

Dokumentiere Modulpfad, gepinnte Hauptversionen, Imageaufbau, Serviceports, ausgeführte Prüfungen und alle Abweichungen von der Zielstruktur. Prüfe insbesondere non-root, Signalhandling, DB-Ausfall und reproduzierbare Generierung.

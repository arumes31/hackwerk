# HackWerk – Einsatzplanung für Hackaufträge

Dieses Paket übersetzt die fachlichen Anforderungen für eine Terminplanungs-App rund um Hackmaschine, Transport, Fahrer, Kunden und Warteliste in eine ausführbare Codex-Arbeitsstruktur.

**Produktname:** `HackWerk`  
**Technische Zielrichtung:** Go, PostgreSQL, Docker, responsive Weboberfläche, deutschsprachige UI (`de-AT`), Zeitzone `Europe/Vienna`.

Der Arbeitsname ist vollständig konfigurierbar. Weitere Namensideen stehen in `reference/naming-ideas.md`.

## Schnellstart der Anwendung

Vorausgesetzt werden Docker Engine und Docker Compose. Browser-Assets liegen als geprüfte statische Dateien im Repository und werden direkt in das Go-Binary eingebettet; weder Build noch Laufzeit benötigen Node, npm oder ein CDN.

```bash
cp .env.example .env
docker compose up --build
```

Danach sind verfügbar:

| Dienst | Adresse | Zweck |
|---|---|---|
| HackWerk | <http://localhost:18533> | responsive Weboberfläche (Host und Container) |
| Liveness | <http://localhost:18533/health/live> | reine Prozessprüfung |
| Readiness | <http://localhost:18533/health/ready> | prüft PostgreSQL |

Die Weboberfläche wird lokal nur auf `127.0.0.1` veröffentlicht, weil die Entwicklungsumgebung öffentlich bekannte Schlüssel und unsichere Cookies verwendet. Wer bewusst aus dem LAN testen will, setzt `APP_BIND_ADDRESS`.

PostgreSQL wird absichtlich nicht am Host veröffentlicht. Compose legt getrennte lokale Rollen für Migrationen und den eingeschränkten App-Betrieb an. Die Kennwörter sind klar als reine Entwicklungswerte markiert.

E-Mail verwendet ausschließlich einen extern konfigurierten SMTP-Dienst für den Versand. Lokal ist Mailversand standardmäßig deaktiviert; automatisierte Tests verwenden einen injizierten Fake-SMTP-Adapter und benötigen weder einen Mail-Container noch echte Zugangsdaten. E-Mail-Empfang ist nicht vorgesehen.

## Entwicklungsbefehle

```bash
make generate          # templ und sqlc reproduzierbar erzeugen
make format            # Go und templ formatieren
make lint              # go vet und gepinnter golangci-lint
make test              # Unit-Tests
make test-integration  # PostgreSQL-Integrationstests
make test-e2e          # Node-freie Browserreisen mit Go/chromedp
make build             # statisches Binary unter bin/hackwerk
make check             # zentrale lokale/CI-Prüfung
make release-check     # zusätzliche Race-, E2E- und Vulnerability-Prüfung
```

Für `make test-e2e` müssen `TEST_DATABASE_URL` und Chrome, Chromium oder Edge vorhanden sein. Bei Bedarf setzt `E2E_BROWSER_PATH` den ausführbaren Browserpfad. Die Browsertests sind Go-Tests und installieren oder verwenden weder Node noch npm.

Das Binary bündelt die Prozessmodi in einem Artefakt:

```text
hackwerk serve
hackwerk worker
hackwerk migrate up|down|status
hackwerk seed-dev
hackwerk admin --help
hackwerk healthcheck
hackwerk healthcheck worker
hackwerk config-check [serve|worker]
hackwerk schema-version
hackwerk version
```

Nach der ersten Migration erzeugt `hackwerk seed-dev` in `development` sechs lokale Zugänge (`admin`, `anna`, `bernd`, `christian`, `doris`, `emil`), fünf getrennte Fahrerprofile mit Beispielsverfügbarkeiten sowie `Hackmaschine 1`, `Transporter 1` und `Anhänger 1` als normale Ressourcen. Kryptographisch zufällige temporäre Passwörter erscheinen ausschließlich beim ersten Seed in stdout und müssen beim ersten Login geändert werden. Für die Admin-CLI werden Passwörter nie als Prozessargument angenommen:

```bash
printf '%s\n' 'ein-langes-temporäres-passwort' | hackwerk admin create --username neu --display-name 'Neuer Fahrer' --role driver --driver
hackwerk admin reset-password --username neu --password-file /sicherer/pfad/passwort.txt
hackwerk admin disable-user --username alt
hackwerk admin list
```

## Sicherheitshinweise für den Betrieb

- Passwortänderung, E-Mail-Änderung, Einrichten oder Entfernen von Authenticator-App und Passkeys sowie neue Recovery-Codes verlangen das aktuelle Passwort. Fehlversuche sind pro Konto begrenzt.
- Anmeldeversuche werden pro Client (IPv6 je /64) und zusätzlich pro Konto über alle Adressen begrenzt. Gezielte Fehlversuche können ein bekanntes Konto dadurch vorübergehend sperren, jeweils bis zum Ende des laufenden Minutenfensters.
- Mit `SESSION_COOKIE_SECURE=true` heißen die Cookies standardmäßig `__Host-hackwerk_session`, `__Host-hackwerk_csrf` und `__Secure-hackwerk_mfa`. Nach dem Update melden sich alle Nutzer einmal neu an. Explizit gesetzte `*_COOKIE_NAME`-Werte bleiben unverändert.
- Produktion verwirft den Entwicklungs-Bestätigungsschlüssel auch unter neuer Schlüssel-ID; jeder Schlüssel in `CONFIRMATION_TOKEN_KEYS` braucht mindestens 32 Byte.
- whisper-server (`compose.voice-host.example.yaml`) hat keine Authentifizierung. Eine Tailscale-ACL soll nur dem App-Host Zugriff auf Port 8080 erlauben, zum Beispiel `{"action": "accept", "src": ["tag:hackwerk-app"], "dst": ["tag:hackwerk-voice:8080"]}`.

## Fehlerbehebung

- Bleibt Readiness auf `503`, zuerst `docker compose logs postgres migrate app` prüfen. Readiness führt selbst keine Migration aus.
- Nach Änderungen an `.templ` oder SQL-Queries `make generate` ausführen. Statisches CSS/JavaScript wird ohne Buildschritt direkt eingebettet.
- Ein bereits initialisiertes lokales Volume behält seine Rollen. Bei bewusst gewünschtem, vollständigem Entwicklungsreset kann das Compose-Volume nach vorheriger Sicherung entfernt werden.
- Produktionskonfigurationen benötigen HTTPS und eine sichere Datenbankverbindung; unsichere Entwicklungsdefaults werden bei `APP_ENV=production` abgewiesen.

## Repository-Struktur

```text
cmd/hackwerk/          Haupteinstiegspunkt für CLI, Server, Worker und Migrationen
internal/
  adapters/postgres/   PostgreSQL-Stores (pgx/v5) und Datenbank-Adapter
  app/                 Applikations- und Business-Logik
  cli/                 Kommandozeilen-Befehle und Admin-Werkzeuge
  config/              Typisierte Konfiguration und Validierung
  logging/             Strukturiertes Logging
  notification/        Benachrichtigungen (SMTP, SMS-Webhook / Sendberry)
  planning/            Planungs- und Routenoptimierungslogik
  voice/               Spracheingabe-Verarbeitung und Entwurfsextraktion
  web/                 HTTP-Routing (chi/v5), Middleware und Handler
web/
  static/              Statisches CSS, JavaScript und Assets (Node-frei)
  templates/           Typsichere HTML-Templates (a-h/templ)
db/                    SQL-Schemata und sqlc-Konfiguration
scripts/               Betriebs- und Automationsskripte
tests/                 E2E- (chromedp) und Integrationstests
reference/             Namensideen, Schemata und Referenztabellen
```

## Produktumfang der Zielversion 1.0

Die Zielversion umfasst:

- lokale Benutzerverwaltung mit Administrator- und Fahrerrolle;
- Kundenakten und mehrere Aufträge pro Kunde;
- Warteliste mit Sortierung und Filterung;
- Tages- und Wochenkalender;
- Drag-and-drop am Desktop und eine mobile Einplanen-Alternative;
- generische Ressourcenverwaltung mit zunächst einer Hackmaschine;
- Fahrer-Verfügbarkeit und Abwesenheiten;
- Terminstatus, Konfliktprüfung und Admin-only-Fixierung;
- E-Mail sowie eine konfigurierbare SMS-Webhook-Anbindung;
- Kundenbestätigung ohne Kundenkonto über einen sicheren Link;
- ICS-Export und private Kalender-Abonnements;
- regelbasierte, erklärbare Planungsvorschläge und Fahrstreckenbewertung;
- Spracheingabe als kontrollierter Entwurf, niemals als ungeprüfte Buchung;
- Dashboard, Audit-Trail, Backups, Healthchecks und produktionsfähige Docker-Images.

## Wichtige Produktentscheidungen

- **Kunde, Auftrag und Termin sind getrennte Objekte.** Ein Kunde kann mehrere Aufträge und historische Termine besitzen.
- **Eine Terminverschiebung wird nicht allein durch die Browseroberfläche entschieden.** Der Server validiert Ressourcen, Fahrer, Berechtigungen und Versionsstand erneut.
- **Drag-and-drop erzeugt einen Terminentwurf.** Nur die explizite Aktion „Termin fixieren & verständigen“ des Administrators macht ihn verbindlich.
- **Alle Fahrer sehen alle geplanten Termine.** Sie können aber keine Termine fixieren oder verschieben.
- **Sprache und Planungsvorschläge sind Assistenzfunktionen.** Sie dürfen weder Kunden noch Termine automatisch ungeprüft speichern oder fixieren.
- **Die Datenstruktur ist nicht auf eine einzelne Maschine fest verdrahtet.** Ressourcen und Zuweisungen sind generisch modelliert.
- **ICS ist in Version 1.0 eine einseitige Veröffentlichung.** Direkte bidirektionale Google-/Microsoft-Synchronisierung ist nicht Teil des Kernumfangs.

## Architektur und Sicherheit

- **Typsichere Templates & Komponenten**: Alle HTML-Seiten werden mit `a-h/templ` zur Compile-Zeit generiert; statische Assets (CSS, JS) sind direkt im Binary eingebettet (kein Node/npm erforderlich).
- **Sichere Session- und Cookie-Verwaltung**: `__Host-` und `__Secure-` Cookies mit `SameSite=Lax`, CSRF-Schutz und servergespeicherten Sessions in PostgreSQL.
- **Transaktionale Outbox**: E-Mail- und SMS-Benachrichtigungen werden atomar über eine relationale Outbox gespeichert und asynchron vom Worker verarbeitet.
- **Resiliente Auditierung**: Alle administrativen Aktionen und Statusänderungen werden unveränderlich auditiert.
- **Healthchecks & Monitoring**: Integrierte Liveness- (`/health/live`), Readiness- (`/health/ready`) und Worker-Healthchecks (`hackwerk healthcheck worker`).

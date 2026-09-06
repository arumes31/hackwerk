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
hackwerk schema-version
hackwerk version
```

Nach der ersten Migration erzeugt `hackwerk seed-dev` in `development` sechs lokale Zugänge (`admin`, `anna`, `bernd`, `christian`, `doris`, `emil`), fünf getrennte Fahrerprofile mit Beispielsverfügbarkeiten sowie `Hackmaschine 1`, `Transporter 1` und `Anhänger 1` als normale Ressourcen. Kryptographisch zufällige temporäre Passwörter erscheinen ausschließlich beim ersten Seed in stdout und müssen beim ersten Login geändert werden. Für die Admin-CLI werden Passwörter nie als Prozessargument angenommen:

```bash
printf '%s\n' 'ein-langes-temporäres-passwort' | hackwerk admin create --username neu --display-name 'Neuer Fahrer' --role driver --driver
hackwerk admin reset-password --username neu --password-file /sicherer/pfad/passwort.txt
hackwerk admin list
```

## Fehlerbehebung

- Bleibt Readiness auf `503`, zuerst `docker compose logs postgres migrate app` prüfen. Readiness führt selbst keine Migration aus.
- Nach Änderungen an `.templ` oder SQL-Queries `make generate` ausführen. Statisches CSS/JavaScript wird ohne Buildschritt direkt eingebettet.
- Ein bereits initialisiertes lokales Volume behält seine Rollen. Bei bewusst gewünschtem, vollständigem Entwicklungsreset kann das Compose-Volume nach vorheriger Sicherung entfernt werden.
- Produktionskonfigurationen benötigen HTTPS und eine sichere Datenbankverbindung; unsichere Entwicklungsdefaults werden bei `APP_ENV=production` abgewiesen.

## Was dieses Paket enthält

- ein kompaktes, dauerhaftes `AGENTS.md` für Codex;
- eine `PLANS.md`-Vorlage für längere Ausführungspläne;
- drei repository-lokale Codex Skills unter `.agents/skills/`;
- normalisierte Produkt-, Architektur-, Sicherheits- und UX-Spezifikationen;
- elf sequenzielle Implementierungsaufträge;
- vier unabhängige Review-Aufträge;
- Gherkin-Akzeptanzszenarien;
- Referenzdateien für Repository-Struktur, Umgebungsvariablen, Status und Rechte;
- einen One-Shot-Masterprompt als Alternative zur empfohlenen schrittweisen Umsetzung.

## Empfohlene Arbeitsweise mit Codex

1. Paket in das leere Repository entpacken und committen.
2. Codex im Repository-Root starten, damit `AGENTS.md` und `.agents/skills` erkannt werden.
3. Für jeden Auftrag einen eigenen Branch oder Git-Worktree und einen eigenen Codex-Thread verwenden.
4. Zuerst ausführen:

   ```text
   $hackplan-implement Implementiere codex/tasks/00-repository-bootstrap.md vollständig.
   ```

5. Danach die Aufgaben `01` bis `11` in Reihenfolge abarbeiten.
6. Nach größeren Meilensteinen die Review-Aufträge `90` bis `93` in separaten Threads ausführen.
7. Jede Codex-Ausgabe anhand des Diffs, der Tests und der Akzeptanzkriterien prüfen, bevor der nächste Auftrag beginnt.

Die Tasks sind bewusst als vertikale, reviewbare Scheiben geschrieben. Der One-Shot-Prompt unter `codex/MASTER_PROMPT.md` ist für einen langen autonomen Lauf gedacht, ist aber weniger gut kontrollierbar als einzelne Aufgaben.

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

## Meilensteine

| Meilenstein | Tasks | Ergebnis |
|---|---:|---|
| M0 – Fundament | 00–01 | Repository, Docker, Datenbank, Authentifizierung, Rollen |
| M1 – Kerndaten | 02–03 | Kunden, Aufträge, Warteliste, Ressourcen, Fahrer-Verfügbarkeit |
| M2 – Planungskern | 04–05 | Kalender, Konflikte, Fixierung, Benachrichtigung, Kundenantwort |
| M3 – Bedienung | 06–07 | Dashboard, responsive UX, ICS-Feeds |
| M4 – Assistenz | 08–09 | Vorschlagslogik, Routenbewertung, Spracheingabe |
| M5 – Betrieb | 10–11 | Security Hardening, Monitoring, Backup, E2E, Release Candidate |


## Auftragslandkarte

| Datei | Ergebnis |
|---|---|
| `00-repository-bootstrap.md` | Go-/Docker-/PostgreSQL-Fundament, CI, Healthchecks |
| `01-foundation-auth-rbac.md` | Login, Sessions, Benutzer/Fahrer, RBAC, Audit |
| `02-customers-orders-waitlist.md` | Kundenakten, Aufträge, Notizen, Warteliste |
| `03-resources-drivers-availability.md` | generische Maschinen/Transportressourcen, Verfügbarkeit |
| `04-calendar-scheduling.md` | Tag/Woche, Drag + mobile Alternative, DB-Konfliktschutz |
| `05-notifications-confirmation.md` | Outbox, externes SMTP, SMS-Webhook, Kundenbestätigung |
| `06-dashboard-mobile.md` | Tagesdashboard und durchgängige responsive UX |
| `07-ics-calendar-feed.md` | ICS-Export und private read-only Feeds |
| `08-planning-suggestions-routing.md` | erklärbare Top-3-Planung und Routenbewertung |
| `09-voice-intake.md` | kontrollierter Sprachentwurf mit Review |
| `10-hardening-observability-backup.md` | Security, Metrics, Backup/Restore, Produktionsbetrieb |
| `11-e2e-release-candidate.md` | vollständige Abnahme und Release Candidate |
| `90`–`93` | unabhängige Security-, Concurrency-, UX- und Betriebsreviews |

## Direkt verwendbare Startprompts

Schrittweise, empfohlen:

```text
$hackplan-implement Implementiere codex/tasks/00-repository-bootstrap.md vollständig.
```

Gesamter Build in einem langen Lauf:

```text
Lies codex/MASTER_PROMPT.md und führe den darin beschriebenen Auftrag vollständig aus.
```

Die detaillierten Standardentscheidungen stehen in `docs/13-decisions-and-assumptions.md`. Produktideen nach V1 stehen in `reference/product-brainstorm.md` und `docs/16-backlog-and-future-ideas.md`.

## Voraussetzungen

- Docker Engine mit Docker Compose;
- Git;
- für lokale Entwicklung optional Go, wenn nicht ausschließlich im Dev-Container gearbeitet wird; Node und npm werden nicht verwendet;
- Codex CLI, IDE-Erweiterung oder Codex in der Desktop-App.

## Verzeichnisübersicht

```text
AGENTS.md                         Dauerhafte Projektregeln für Codex
PLANS.md                          Vorlage für lange Ausführungspläne
.agents/skills/                   Repository-lokale Codex Skills
codex/MASTER_PROMPT.md            Gesamter Build in einem Auftrag
codex/tasks/                      Sequenzielle Implementierungs- und Review-Aufträge
docs/                             Produkt- und Technik-Spezifikationen
acceptance/                       Gherkin-Akzeptanzszenarien
reference/                        Strukturen, Konfigurationen und Tabellen
```

## Definition von „fertig“

Eine Aufgabe ist erst abgeschlossen, wenn:

- Implementierung und Migrationen vollständig sind;
- relevante Unit-, Integrations- und Browsertests vorhanden sind;
- `make check` erfolgreich läuft;
- Dokumentation und OpenAPI-Vertrag aktualisiert wurden;
- keine ungeprüften TODO-Platzhalter im vereinbarten Scope verbleiben;
- Codex den Diff selbst gegen die Akzeptanzkriterien geprüft hat;
- Sicherheits- und Berechtigungsregeln serverseitig getestet sind.

Die vollständige Definition steht in `docs/15-definition-of-done.md`.

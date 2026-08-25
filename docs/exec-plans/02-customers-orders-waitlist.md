# ExecPlan 02 – Kunden, Aufträge und Warteliste

Status: abgeschlossen

## Ziel und sichtbares Ergebnis

Admin und Fahrer können getrennte Kundenakten und beliebig viele Hackaufträge erfassen, bearbeiten und mit append-only Bemerkungen auf einer filter- und sortierbaren Warteliste verwalten. Archivierung, Prioritätsänderung und Entfernen bleiben serverseitig Admin-only. Die Erfassung ist transaktional, auf 360 px bedienbar und zeigt Transportfelder nur für Transportaufträge; der Browser benötigt dafür weder Node noch npm.

## Kontext und betroffene Bereiche

- Task: `codex/tasks/02-customers-orders-waitlist.md`
- Regeln: `docs/03-domain-model.md`, `docs/05-rbac.md`, `docs/06-ux-and-responsive.md`, `docs/07-api-and-integrations.md`, `docs/10-security-privacy.md`, `docs/11-test-strategy.md`
- Datenbank: Migration `00003`, Queries unter `db/queries/customers.sql`, generierter sqlc-Code und PostgreSQL-Adapter
- Anwendung: `internal/customers`, `internal/adapters/postgres`, `internal/web`, `web/templates/customers.templ`, `web/assets/static/app.js`
- Verträge/Testdaten: `openapi/openapi.yaml`, `reference/seed-scenario.md`, Unit-, HTTP-, Integrations- und Browser-E2E-Tests

## Annahmen und feste Entscheidungen

- Die Umbenennung auf `HackWerk` ändert sichtbare UI-/Runtime-Namen. Der kompatible Go-Modulpfad, bestehende Datenbankrollen/-namen und Cookie-Namen bleiben in diesem Task unverändert.
- Kontaktwertsuche läuft als CSRF-geschützter POST, damit Telefonnummern und E-Mail-Adressen nicht in URL, Historie oder Access Logs gelangen.
- Kleine progressive UI-Interaktionen bestehen aus direkt eingebettetem browsernativem JavaScript. Browser-E2E verwendet das Chrome-DevTools-Protokoll über Go/chromedp und eine lokal vorhandene Chrome-/Edge-Installation.
- `github.com/chromedp/chromedp v0.16.0` und das dazugehörige `cdproto` sind ausschließlich Testabhängigkeiten hinter dem Build-Tag `e2e`; sie gelangen nicht in das Produktionsbinary.

## Risiken

- Parallel erzeugte lesbare Auftragsnummern dürfen sich nicht wiederholen.
- Kunde, Auftrag, Wartelisteneintrag, optionale Notiz und Audit müssen beim Intake vollständig committen oder vollständig zurückrollen.
- Browser-RBAC darf nicht die einzige Schutzschicht für Archivierung und Wartelistenadministration sein.
- Kontakt-/Adressdaten und Freitext dürfen weder technische Logs/Auditfelder noch unsichere HTML-/URL-Kontexte erreichen.
- Chipping-only-Aufträge dürfen keine widersprüchlichen Transportwerte persistieren; externe Transporte benötigen eine explizite Bestätigung.
- Mobile Nutzer benötigen vollständig sichtbare Formulare/Karten ohne horizontales Scrollen.

## Umsetzungsschritte

1. Schema, Constraints, Nummernsequenz, append-only Notizen und sqlc-Queries ergänzen.
2. Domänenvalidierung für Kontakte, Feldgrenzen, Mengen, Dauern, Transport und Filter-Allowlist implementieren.
3. Transaktionalen PostgreSQL-Adapter mit optimistic concurrency, Archivierungsregeln und minimiertem Audit implementieren.
4. Application Service, responsive Formulare, Kundenakte, Warteliste und progressive Transportanzeige implementieren.
5. Seed-Szenarien und OpenAPI-Vertrag vervollständigen.
6. Unit-, authentifizierte HTTP-, PostgreSQL-Integrations- und Node-freie Browser-E2E-Tests ergänzen.
7. Generierungs-, Format-, Lint-, Test-, Race-, Build- und Containergates ausführen und Diff selbst prüfen.

## Datenbankänderungen

- `customers`, `jobs`, `job_number_counters`, `job_notes` und `waitlist_entries` bleiben getrennte Tabellen mit restriktiven Fremdschlüsseln.
- Eine Jahreszeile in `job_number_counters` vergibt `HW-YYYY-NNNNNN` atomar per `INSERT ... ON CONFLICT ... RETURNING`; das Jahr wird in PostgreSQL für `Europe/Vienna` bestimmt.
- Ein partieller Unique-Index erlaubt höchstens einen aktiven Wartelisteneintrag pro Auftrag und bewahrt entfernte Historie.
- Trigger verhindern `UPDATE` und `DELETE` von `job_notes`.
- Ein Check-Constraint erzwingt konsistente externe Transportbestätigung.
- Versionen auf Kunden/Aufträgen sichern Änderungen optimistic; Archivierung und abhängige Wartelistenänderungen laufen in derselben Transaktion.
- Up/Down/Up wird in den Integration- und Browserfixtures gegen PostgreSQL 18 ausgeführt.

## Testplan

- Unit: Kontaktvalidierung, Feldgrenzen, Transportinvarianten, Dauer/Volumen, Filter-Allowlist und Maps-Link.
- HTTP: echte Session-/CSRF-Middleware, Fahrer-/Admin-Rechte, PRG, 409, 422, XSS-Escaping und sichere Maps-URL.
- Integration: atomarer Intake/Rollback, konkurrierende Nummernvergabe, stale Version, Unique-Warteliste, append-only Trigger, Archivierung, Duplikatwarnung und serverseitige Filter/Sortierung.
- Browser: echter Headless Edge/Chrome, Fahrer-Intake, progressive Transportfelder, Admin-Suche/-Bearbeitung, direkter verbotener Fahrer-POST, 360-px-Warteliste und JavaScript-Ausnahmen.
- Manuell/Gates: Generierungsdrift, OpenAPI-YAML, Go-Modulverifikation, Race, statisches Binary und Docker-Smoke.

## Fortschritt

- [x] Schema, Migration und sqlc-Queries
- [x] Domäne, Service und PostgreSQL-Adapter
- [x] responsive UI, progressive Transportfelder und RBAC
- [x] Seed und OpenAPI
- [x] Unit-, HTTP- und PostgreSQL-Integrationstests
- [x] Node-freier Browser-E2E mit lokalem Edge
- [x] vollständige Task-02-Gates und Diff-Selbstreview

## Entdeckungen und Entscheidungen während der Umsetzung

- 2026-08-25: Der erste PostgreSQL-18-Lauf zeigte, dass Goose die PL/pgSQL-Triggerfunktion ohne Parsergrenzen am internen Semikolon trennt. Migration 00003 verwendet deshalb `StatementBegin`/`StatementEnd`; der fehlgeschlagene Lauf wurde transaktional vollständig zurückgerollt.
- 2026-08-25: Der reale Paralleltest bestätigt 16 eindeutige Auftragsnummern bei 16 konkurrierenden Transaktionen.
- 2026-08-25: Direkte Serviceaufrufe werden zusätzlich zu Datenbank-FKs gegen archivierte Kunden geschützt, damit der fachliche Fehler stabil bleibt.
- 2026-08-25: Optionale Initialnotizen erhalten einen eigenen minimierten Auditdatensatz; der Notiztext selbst wird nicht protokolliert.
- 2026-08-25: Der Browser-E2E läuft mit chromedp v0.16.0 gegen den lokal installierten Edge und PostgreSQL 18.6. Playwright wurde nicht aufgenommen, damit das Repository ohne Node/npm bleibt.
- 2026-08-25: Die echte Browserreise prüft die Kontaktwertsuche als POST und bestätigt, dass der Suchwert nicht in der URL erscheint.
- 2026-08-25: Der abschließende Produktreview fand zwei kleine Punkte: sensible geschützte Seiten erhielten nicht durchgängig `Cache-Control: no-store`, und der vorbereitete Admin-Planungsbutton war auch für Fahrer sichtbar. Beides wurde mit HTTP-Regressionstests korrigiert; Blocker, High- oder verbleibende Medium-Findings bestehen für Task 02 nicht.
- 2026-08-25: Eine Browsernavigation direkt nach dem POST-Suchergebnis konnte im CDP-Test mit `ERR_ABORTED` flaken. Der Test klickt nun den tatsächlich gerenderten Kundenlink und wartet getrennt auf die Zielseite; zwei aufeinanderfolgende Läufe waren stabil grün.

## Abschlussnachweis

Erfolgreich ausgeführt wurden der driftfreie templ-/sqlc-Generatorcheck, `go mod verify`, OpenAPI-YAML-Parsing, `go test -count=1 ./...`, `go vet ./...`, golangci-lint (`0 issues`), statischer Windows-Build, `go test -count=1 -tags=integration -v ./tests/integration/...` gegen PostgreSQL 18.6 und `go test -race ./...` im offiziellen Go-1.27-Bookworm-Container. Der Node-freie Edge-E2E war nach der Flake-Korrektur einmal verbose und zweimal direkt hintereinander grün; er prüft realen Datenbankzustand, 403-RBAC, POST-Suche, Transport-UX, JavaScript-Ausnahmen und den 360-px-Viewport.

Das finale Scratch-Image `hackwerk:task02-gate` wurde aus Go 1.27 gebaut und startet `/hackwerk`; Imagekonfiguration ist UID/GID 65532, Größe rund 5,8 MB. Repository-Audit findet keine `package.json`, Lockdatei oder `node_modules`. Alle kurzlebigen PostgreSQL-Testcontainer verwendeten `tmpfs` und wurden mit `--rm` entfernt. Selbstreview-Empfehlung: Task 02 ist ohne offene Blocker/High/Medium-Findings bereit für Task 03.

# ExecPlan 04 – Kalender, Terminplanung und konfliktfreie Reservierung

Status: in Arbeit

## Ziel und sichtbares Ergebnis

HackWerk erhält einen responsiven zentralen Tages-/Wochenkalender. Administratoren planen Wartelistenaufträge per Desktop-Drag-and-drop oder über dieselbe vollständig bedienbare mobile Einplanungsmaske, weisen Fahrer und beliebig viele passende Ressourcen zu, verschieben/vergrößern Vorschläge und fixieren Termine ausdrücklich. Fahrer sehen alle Termine und Details, können die Planung aber weder über die Oberfläche noch per direktem Request verändern. PostgreSQL verhindert Doppelbelegungen auch bei parallelen Requests.

## Kontext und betroffene Bereiche

- Task: `codex/tasks/04-calendar-scheduling.md`
- Regeln: `docs/03-domain-model.md`, `docs/04-status-state-machine.md`, `docs/05-rbac.md`, `docs/06-ux-and-responsive.md`, `docs/07-api-and-integrations.md`, `docs/08-planning-engine.md`, `docs/10-security-privacy.md`, `docs/11-test-strategy.md`
- Akzeptanz: `acceptance/calendar.feature`, `acceptance/permissions.feature`, `reference/status-transitions.csv`
- Neues Domänenpaket `internal/appointment`; Migration 00005, sqlc-Queries und PostgreSQL-Adapter
- Neue HTML-/JSON-Routen unter `/calendar` und `/api/calendar`; lokal gebündeltes FullCalendar Standard ohne Node/npm und ohne Laufzeit-CDN

## Annahmen und feste Entscheidungen

- `draft` reserviert keine Fahrer oder Ressourcen. `proposal` und `fixed` besitzen aktive Reservierungszeilen und blockieren exklusive Fahrer/Ressourcen über GiST-Exclusion-Constraints. Stornierung und Abschluss deaktivieren Reservierungen atomar.
- Der Appointment-Zeitraum ist halb-offen `[starts_at, ends_at)`. Direkt angrenzende Termine kollidieren daher nicht; Buffer werden in den tatsächlich reservierten Bereich eingerechnet.
- Fahrerzuweisungen sind immer exklusiv. Nur Ressourcen mit `exclusive=true` blockieren exklusiv; nicht exklusive Ressourcen bleiben mehrfach zuweisbar.
- Verfügbarkeit wird vor jeder Aktivierung oder Zeitänderung serverseitig aufgelöst. Ein Admin darf nur Verfügbarkeitskonflikte mit nicht leerer Begründung übersteuern; Fahrer-/Ressourcenüberschneidungen sind nicht übersteuerbar.
- Ein Auftrag kann höchstens einen Termin in `draft`, `proposal` oder `fixed` besitzen. `cancelled` und `completed` bleiben Historie und geben eine spätere Neuplanung frei.
- Fixierung setzt Auftrag/Warteliste/Termin, Audit und `appointment.fixed`-Outboxevent in einer Transaktion. Es wird in Task 04 weder E-Mail noch SMS gesendet.
- Verschieben eines fixierten Termins setzt dessen Bestätigungsstatus auf `pending` und erzeugt ein minimiertes `appointment.moved`-Event. Die Tokenwiderruf-/Versandmechanik ergänzt Task 05 auf dieser Semantik.
- FullCalendar wird als geprüfte, lokal gespeicherte Standard-Distribution eingebunden. Die serverseitige mobile Einplanungsmaske und Agenda funktionieren unabhängig von Drag-and-drop und JavaScript.
- Lokale Formulareingaben werden streng in `Europe/Vienna` aufgelöst. Nicht existente und mehrdeutige DST-Ortszeiten werden als validierbarer Fehler abgewiesen; gespeichert wird UTC.

## Risiken

- Eine getrennte Vorprüfung und spätere Speicherung würde bei parallelem Fixieren/Move/Resize eine Race-Lücke erzeugen. Die Datenbankconstraints bleiben deshalb letzte Autorität und ihre Fehler werden deterministisch übersetzt.
- Reservierungszeilen, Appointment-Version, Auftrag, Warteliste, Audit und Outbox dürfen nicht teilweise committen.
- Ein stale Browser-Event darf keine neuere Planung überschreiben; jede Mutation prüft `version` und liefert 409 mit Reloadhinweis.
- Direkt aufgerufene Mutationsendpunkte dürfen Fahrer nicht durchlassen, auch wenn Buttons im UI verborgen sind.
- Kalender-Range, Details und Konfliktantworten müssen begrenzt und datensparsam bleiben; Kontakte erscheinen nicht im allgemeinen Eventfeed.
- DST-Grenzen, Buffer und halb-offene Bereiche können sichtbare und gespeicherte Zeiten auseinanderlaufen lassen, falls sie nicht zentral berechnet werden.
- FullCalendar-Assets dürfen weder Node/npm noch CDN-Laufzeitabhängigkeit einführen.

## Umsetzungsschritte

1. Migration 00005 mit Terminen, Zuweisungen, aktiven Reservierungsbereichen, partiellen Unique-/Exclusion-Constraints und Outboxbasis erstellen.
2. Appointment-Domänenmodell, Transitionen, Zeit-/Transport-/Zuweisungsvalidierung und stabile Fehlersemantik implementieren.
3. Transaktionalen PostgreSQL-Store und Use Cases für Draft, Proposal, Move, Resize, Assign, Fix, Cancel, Complete und Range-/Konfliktabfragen implementieren.
4. RBAC, Application Wiring, HTML-/JSON-Routen, CSRF, Versionsprüfung und datensparsame Fehler-/Eventantworten ergänzen.
5. FullCalendar Standard lokal pinnen und die responsive deutsche Kalenderoberfläche samt Warteliste, Detailpanel, Revert und mobiler Einplanungsalternative implementieren.
6. Seed, OpenAPI sowie Betriebs-/Architekturdokumentation aktualisieren.
7. Unit-, HTTP-, PostgreSQL-Integrations-, DST- und Node-freie Browser-E2E-Tests ergänzen.
8. Generierungs-, Format-, Lint-, Unit-, Integration-, Race-, Browser-, Build- und Container-Gates sowie Diff-Selbstreview ausführen.

## Datenbankänderungen

- `appointments` referenziert Auftrag, hält Lifecycle-/Confirmationstatus, Start/Ende, Buffer, Override/Fixierungs-/Abbruch-/Abschlussmetadaten und `version`; ein Check erzwingt `ends_at > starts_at`.
- Ein partieller Unique-Index auf `job_id` für `draft`, `proposal`, `fixed` erzwingt höchstens einen aktiven Termin pro Auftrag.
- `appointment_drivers` hält Primärkennzeichen, den mit Buffer erweiterten `reserved_range` und `active`; ein partieller Unique-Index erlaubt höchstens einen Primärfahrer je Termin. Ein GiST-Constraint verhindert aktive Überschneidungen pro Fahrer.
- `appointment_resources` hält Zweck, `reserved_range` und `active`; nur Zeilen für exklusive Ressourcen werden aktiv markiert und durch GiST gegen Überschneidung geschützt.
- Trigger/Checks halten Reservierungsrange und Appointment-Zeitraum konsistent; alle Änderungen erfolgen unter einer Appointment-Zeilensperre mit Versionsvergleich.
- `outbox_events` enthält stabilen Eventtyp, Aggregat-ID, minimiertes JSON-Payload, Idempotenzschlüssel, Status/Versuchsmetadaten und Zeitstempel; Task 05 erweitert die Zustelladapter ohne Schemaersatz.
- Down entfernt ausschließlich Task-04-Objekte. Bestehende Kunden-, Auftrags-, Fahrer-, Ressourcen- und Auditdaten bleiben erhalten.

## Testplan

- Unit: erlaubte/verbotene Transitionen, Versionen, Dauer/Buffer, `[start,end)`, Transportpflicht, Ressourcentyp, Availability/Override, Rechte und Range-Limits.
- DST: normale Vienna-Zeiten sowie Frühjahrslücke und doppelte Herbststunde; UTC-Speicherung und Tages-/Wochenrendering.
- HTTP: echte Session/CSRF, Adminmutationen, Fahrer read-only auch direkt, Mass-Assignment-Abwehr, 400/403/404/409/422 und redigierter Eventfeed.
- Integration: Migration Up/Down/Up, direkt angrenzende Intervalle, Fahrer-/Ressourcen-Exclusion, zweites Gerät, aktiver-Termin-Unique, stale Version, paralleles Fixieren/Move/Resize sowie vollständiger Rollback von Auftrag/Warteliste/Audit/Outbox.
- Browser: externer Wartelisten-Drag, mobile Einplanung bei 360 px, Move/Resize und `revert()` bei Konflikt, explizite Fixierung, kombinierte Statusbadges und Fahreransicht ohne Planungskontrollen; ohne Node/npm.
- Manuell: lokale Assets ohne Netzwerk, TAG/WOCHE/Heute/Navigation, Detail-/Maps-Link, Fehlerwiederherstellung und Container-Port 18533.

## Fortschritt

- [x] Spezifikation, Statusmodell, RBAC, UX/API/Security und Akzeptanzszenarien auditiert
- [ ] Migration und Queries
- [ ] Domänenpaket, Store und Use Cases
- [ ] HTTP/UI/FullCalendar
- [ ] Seed/OpenAPI/Dokumentation
- [ ] Unit-/HTTP-/Integration-/Browsertests
- [ ] Gesamtgates und Selbstreview

## Entdeckungen und Entscheidungen während der Umsetzung

- 2026-08-25: FullCalendar Standard wird in der aktuellen stabilen Global-Bundle-Variante lokal vendort. Die Standardplugins enthalten die benötigten Zeitraster-, Listen- und Interaktionsfunktionen; Premium-Ansichten werden nicht verwendet.
- 2026-08-25: Auf Benutzervorgabe bleibt die gesamte Toolchain Node-/npm-frei. Browserlogik wird als kleine lokale Vanilla-JavaScript-Schicht implementiert und E2E läuft mit dem bestehenden Go/chromedp-Setup.
- 2026-08-25: Der Runtime-Webport ist innerhalb und außerhalb des Containers einheitlich `18533`.

## Abschlussnachweis

Wird während der Umsetzung mit Befehlen, Parallelitätsergebnissen, Browserflüssen, Container-Smokes und Diff-Selbstreview fortgeschrieben.

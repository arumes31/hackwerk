# ExecPlan 03 – Ressourcen, Fahrer und Verfügbarkeit

Status: abgeschlossen

## Ziel und sichtbares Ergebnis

HackWerk verwaltet Maschinen/Transportmittel als beliebig erweiterbare Ressourcen und Fahrer als eigenständige, optional mit Login verknüpfte Profile. Fahrer pflegen ausschließlich ihre eigene wiederkehrende Verfügbarkeit und Ausnahmen mobil; Administratoren verwalten alle Profile/Ressourcen und sehen eine normalisierte Vorschau. Ein testbarer Resolver übersetzt Vienna-Wochenregeln und Ausnahmen in UTC-Intervalle, wobei fehlende oder zeitlich mehrdeutige Regeln sicher als nicht verfügbar gelten.

## Kontext und betroffene Bereiche

- Task: `codex/tasks/03-resources-drivers-availability.md`
- Regeln: `docs/03-domain-model.md`, `docs/05-rbac.md`, `docs/06-ux-and-responsive.md`, `docs/08-planning-engine.md`, `docs/10-security-privacy.md`
- Akzeptanz: `acceptance/availability.feature`, `reference/permissions-matrix.csv`, `reference/seed-scenario.md`
- Neue Domänenpakete: `internal/resource` für Maschinen/Fahrzeuge, `internal/driver` für Fahrerprofile und Availability
- Datenbank/HTTP/UI: Migration 00004, sqlc-Queries, PostgreSQL-Adapter, `/resources`, `/drivers`, `/availability` und minimale JSON-Overlays

## Annahmen und feste Entscheidungen

- Fahrer und Ressourcen bleiben getrennte Domänenobjekte; deshalb entstehen die singular benannten Go-Pakete `driver` und `resource` statt eines gemeinsamen generischen „operations“-Pakets.
- Kapazitätsmetadaten werden als typisierte Felder (`volume_m3`, `payload_kg`, `seats`) validiert und als JSONB gespeichert. Die UI bietet keine freie JSON-Textbox.
- Wochenregeln speichern lokale Vienna-Uhrzeiten und Datumsgrenzen. Die Auflösung nimmt ausschließlich UTC-Zeiträume entgegen und gibt UTC zurück.
- Fehlende Regel ist `unavailable`. Nicht existierende oder mehrdeutige lokale Regelgrenzen an einem DST-Wechseltag werden nicht geraten: Die Auflösung wird sichtbar mit einem redigierten Ortszeitfehler abgewiesen, bis die Regel korrigiert ist; es wird niemals stillschweigend Verfügbarkeit erzeugt.
- Ausnahmepriorität ist sicherheitsorientiert und deterministisch: `sick`, `vacation`, `unavailable`, `other` übersteuern `available_override`; jede Ausnahme übersteuert eine wiederkehrende Regel. Details bleiben nur im Admin-/Eigentümerkontext sichtbar.
- All-day-Ausnahmen verwenden ein lokales Vienna-Datum; partielle Ausnahmen speichern einen expliziten UTC-Zeitraum.
- Ein Fahrerrequest bestimmt das Zielprofil ausschließlich aus `Actor.DriverID`. Fremde IDs sind nur über Admin-Routen und `availability.update_other` zulässig.

## Risiken

- Wiederkehrende Regeln können sich in Uhrzeit und Gültigkeitsdatum überschneiden; dies muss auch unter Parallelität auf Datenbankebene verhindert werden.
- Benutzer-/Fahrerverknüpfung muss eins-zu-eins bleiben und Entkopplung/Wechsel darf keine Session- oder Historienannahme brechen.
- DST-Lücken und doppelte lokale Uhrzeiten dürfen weder verschoben noch mit falscher Dauer als verfügbar erscheinen.
- Krankheits- und interne Notizen dürfen nicht in allgemeinen JSON-Overlays, technische Logs oder minimierte Auditmetadaten gelangen.
- Ressourcendeaktivierung und Fahrerarchivierung dürfen keine Historie löschen und Task 04 keine Singleton-Hackmaschine aufzwingen.
- Optimistic locking und Objekt-Scoping müssen direkte Fahrerrequests auf fremde Regeln/Ausnahmen abweisen.

## Umsetzungsschritte

1. Migration 00004 mit generischen Ressourcen, Fahrerprofil-Erweiterung, Regeln, Ausnahmen, Versionen und Überschneidungsconstraints erstellen.
2. Domänenmodelle/Validierung und den deterministischen Vienna-UTC-Resolver mit DST-Tests implementieren.
3. sqlc-Queries und transaktionale PostgreSQL-Stores inklusive minimiertem Audit und optimistic concurrency implementieren.
4. Application Wiring, HTML-/JSON-Routen und responsive deutsche UI mit getrennten Fahrer-/Adminpfaden implementieren.
5. Development-Seed um Hackmaschine, Transporter, Anhänger und reproduzierbare Verfügbarkeiten erweitern.
6. OpenAPI und Betriebs-/Testdokumentation aktualisieren.
7. Unit-, HTTP-, PostgreSQL-Integrations- und Node-freie Browser-E2E-Tests ergänzen.
8. vollständige Generierungs-, Lint-, Test-, Race-, Build- und Containergates sowie Diff-Selbstreview ausführen.

## Datenbankänderungen

- `resources` enthält Typ, Name, Aktivität, Exklusivität, validiertes JSONB, interne Notiz und `version`.
- `drivers` aus Migration 00002 bleibt das Fahrerprofil; Migration 00004 ergänzt Feldgrenzen/Indizes, ohne Loginzwang.
- `availability_rules` enthält ISO-Wochentag, lokale Start-/Endzeit, Gültigkeitsdaten, Status, Notiz und `version`. Ein GiST-Exclusion-Constraint kombiniert Fahrer, Wochentag, Datumsbereich und Minutenbereich und verhindert überlappende Regeln auch bei parallelen Writes.
- `availability_exceptions` enthält entweder genau ein lokales ganztägiges Datum oder einen UTC-Zeitraum, den Typ, interne Notiz und `version`.
- Alle FKs sind restriktiv; Deaktivierung ersetzt physisches Löschen historisch relevanter Objekte.
- Down entfernt nur Task-03-Objekte/Constraints und lässt die ursprüngliche Fahrer-/Benutzerbasis intakt.

## Testplan

- Unit: Ressourcentyp/Metadaten, Fahrerfelder, Regeln, Ausnahmeformen, Overlay-Priorität, Lücken, `IsAvailable`, Bereichslimits.
- DST: Vienna-Frühjahrslücke 29. März 2026, doppelte Stunde 25. Oktober 2026, normaler Sommer-/Wintertag und all-day-Tageslänge.
- HTTP: echte Session/CSRF, DriverID-Scoping, direkte Fremd-ID 403, Admin-Ressourcen-/Fahrerverwaltung, redigierte JSON-Provenienz und 409.
- Integration: Migration Up/Down/Up, Unique User/Driver, Rule-Exclusion auch konkurrierend, optimistic edits, Deaktivierung, Audit ohne Notizdetails.
- Browser: Fahrer ändert eigene Woche/Ausnahme, fremde Mutation scheitert, Admin sieht/ändert alle und legt eine weitere Ressource an; 360-px-Smoke ohne Node/npm.

## Fortschritt

- [x] Spezifikation, vorhandenes Fahrer-/Auth-Schema und Berechtigungen auditiert
- [x] Migration und Queries
- [x] Domänenpakete und Resolver
- [x] PostgreSQL-Stores und Application Wiring
- [x] HTTP/UI/JSON
- [x] Seed/OpenAPI/Dokumentation
- [x] Unit-/HTTP-/Integration-/Browsertests
- [x] Gesamtgates und Selbstreview

## Entdeckungen und Entscheidungen während der Umsetzung

- 2026-08-25: Das bestehende Fahrerprofil aus Migration 00002 ist bereits optional und per Unique-FK mit einem Benutzer verknüpft. Task 03 erweitert diese Basis statt eine zweite Fahrertabelle anzulegen.
- 2026-08-25: `btree_gist` ist seit Migration 00001 verfügbar und kann den wiederkehrenden Regel-Overlap zusammen mit Fahrer/Wochentag/Daterange absichern.
- 2026-08-25: Das Naming-Review führte zu getrennten singularen Paketen `driver` und `resource`; dadurch bleibt die unverhandelbare Trennung Fahrer/Ressource auch in der Go-API sichtbar.
- 2026-08-25: Die Adminoberfläche verwendet für Login-Verknüpfung eine validierte Auswahlliste aktiver Fahrerbenutzer. Bereits verknüpfte Logins sind deaktiviert; Fahrer ohne Login bleiben vollständig planbar.
- 2026-08-25: Die JSON-Overlays serialisieren Status und Provenienz, aber niemals `internal_note`. Allgemeine Fremdfahrer-Endpunkte existieren nicht; Eigentümer- und Adminautorisierung wird im Service erzwungen.
- 2026-08-25: Auf Benutzervorgabe verwendet das gesamte Runtime-Setup innen und außen Port `18533`. Die App besitzt ein separates öffentliches Ingressnetz, während PostgreSQL ausschließlich im internen Backend bleibt.

## Abschlussnachweis

- Generierung/Format: `go tool templ generate`, `go tool sqlc generate -f db/sqlc.yaml` und `gofmt`; SHA-256-Vergleich vor/nach erneuter Generierung meldet keine Drift.
- Unit/HTTP: `go test ./...` vollständig grün. Enthalten sind fehlende-Regel-Default, Ausnahmenpriorität, `IsAvailable`, Ressourcenmetadaten, eigener DriverID-Scope, direkter Fremdzugriff 403, CSRF, typed Resource-Input und JSON-Notizredaktion.
- DST: automatisiert geprüft werden die Vienna-Frühjahrslücke am 29.03.2026, die doppelte Herbststunde am 25.10.2026 sowie 23-/25-Stunden-Dauern ganztägiger Ausnahmen. Mehrdeutige/nicht existente Eingaben liefern `ErrLocalTime` statt stiller Verschiebung.
- PostgreSQL 18.6: `go test -count=1 -tags=integration ./tests/integration/...` grün. Migration Up/Down/Up, typisierte Ressourcen, User/Fahrer-Unique, konkurrierende überlappende Regeln (genau ein Erfolg/ein Konflikt), stale Updates und Audit ohne Krankheitsnotiz sind abgedeckt.
- Browser: `go test -v -count=1 -tags=e2e ./tests/e2e/...` grün gegen Headless Edge. Fahrer pflegt eigene Woche, Fremdrequest liefert 403, Admin sieht/ändert beide Fahrer, legt `Hackmaschine 2` an und die Ressourcenseite hat bei 360 px keinen horizontalen Overflow. Task-02-Reise bleibt ebenfalls grün.
- Race/Lint/Build: Linux Go 1.27 `go test -race ./...`, `go vet ./...`, `go tool golangci-lint run ./...` (0 Issues) und statischer Windows-Build sind erfolgreich.
- Seed: erster isolierter Lauf erzeugt drei normale Ressourcen und Verfügbarkeitsszenarien; zweiter Lauf erzeugt null Duplikate und verändert keine Passwörter.
- OpenAPI: YAML 0.3.0 ist parsebar und dokumentiert beide strikt auf 90 Tage begrenzten Availability-Overlays ohne interne Notiz.
- Container: Scratch-Image circa 5,9 MB, UID/GID 65532, ExposedPort `18533/tcp`, Entrypoint `/hackwerk`. Compose enthält nur PostgreSQL, Migrate, App und Worker; `0.0.0.0:18533->18533/tcp`, Live=`live`, Ready=`ready`, Migration 00004 aktuell. Kein lokaler Maildienst, kein E-Mail-Empfang und keine Node-/npm-Artefakte.
- Selbstreview nach HackWerk-Regeln: keine offenen Blocker-, High- oder Medium-Findings. Service-RBAC ist unabhängig von sichtbaren Buttons, Regelüberschneidung wird in PostgreSQL verhindert, UTC/Vienna-Grenzen sind explizit, Notizen/Diagnosen fehlen in Logs/Audit/JSON und Ressourcen sind nicht auf eine einzelne Hackmaschine fest verdrahtet.

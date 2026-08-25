# AGENTS.md – HackWerk

## Auftrag

Baue und pflege HackWerk als sicheren, responsiven modularen Go-Monolithen für Kunden, Hackaufträge, Warteliste, Fahrer-Verfügbarkeit, Ressourcen, Terminplanung, Benachrichtigung, Kalenderfeeds, Planungsvorschläge und kontrollierte Spracheingabe.

Lies vor einer Änderung mindestens die betroffene Task-Datei sowie die referenzierten Dokumente in `docs/`. Für Arbeiten mit mehreren Modulen erstelle oder aktualisiere einen Ausführungsplan nach `PLANS.md`.

## Unverhandelbare Produktregeln

1. Nur Administratoren dürfen Termine planen, verschieben, fixieren, absagen oder neu öffnen.
2. Alle Fahrer sehen alle geplanten Termine, nicht nur eigene Zuweisungen.
3. Fahrer dürfen Kunden und Aufträge anlegen/bearbeiten, Wartelisteneinträge erzeugen, eigene Verfügbarkeit pflegen und interne Bemerkungen ergänzen.
4. Drag-and-drop erzeugt höchstens einen Entwurf/Vorschlag. Eine separate Admin-Aktion fixiert den Termin und löst Benachrichtigungen aus.
5. Spracheingabe erzeugt nur einen prüfbaren Entwurf. Niemals automatisch Kunde, Auftrag oder Termin ungeprüft speichern oder fixieren.
6. Planungsvorschläge dürfen niemals selbstständig Termine fixieren.
7. Kunde, Auftrag, Termin, Fahrer und Ressource bleiben getrennte Domänenobjekte.
8. Keine Logik darf genau eine Maschine hardcoden. Eine initiale Hackmaschine wird als Datensatz angelegt.
9. Zeiten werden als `timestamptz`/UTC gespeichert und in `Europe/Vienna` dargestellt. Datums- und DST-Fälle testen.
10. Kundendaten, Tokens, Passwörter, Audio und Nachrichtentexte dürfen nicht ungefiltert in technischen Logs erscheinen.

## Zielarchitektur

- Go `1.27.x`, Standardbibliothek bevorzugen.
- HTTP-Routing mit `chi/v5`.
- PostgreSQL `18.x`, Zugriff mit `pgx/v5` und generierten `sqlc`-Queries.
- Migrationen mit einem im Go-Modul fixierten Migrationstool.
- Serverseitige Komponenten mit `templ`.
- `htmx 2.x` für Hypermedia-Interaktionen.
- Kein Node, npm oder JavaScript-Buildschritt. Komplexe Browserinteraktionen verwenden kleines browsernatives JavaScript; notwendige Fremdbibliotheken werden fest versioniert, prüfsummengesichert und lokal eingebettet.
- FullCalendar Standard-Pakete für Tages-/Wochenansicht, externe Wartelisten-Drags, Verschieben und Größenänderung. Keine Premium-Ansichten voraussetzen.
- Ein Go-Binary mit Subcommands `serve`, `worker`, `migrate`, `seed-dev`, `admin`, `healthcheck`.
- PostgreSQL-basierte Sessions und Transactional Outbox; kein Redis ohne neue ADR.
- Assets lokal bündeln; keine Laufzeitabhängigkeit von CDNs.

## Erwartete Repository-Struktur

Siehe `reference/repository-tree.txt`. Domänenpakete liegen unter `internal/`. HTTP-Handler enthalten keine Geschäftslogik. SQL bleibt in `db/queries`; generierter Code wird nicht manuell editiert.

## Abhängigkeitsrichtung

`web/handlers -> application services -> domain -> ports`

Adapter für PostgreSQL, E-Mail, SMS, Routing, Geocoding, Sprache und Kalender liegen außen. Domänenlogik kennt keine HTTP-, SQL- oder Providerdetails.

## Daten- und Nebenwirkungsregeln

- Statuswechsel ausschließlich über explizite Service-Methoden und validierte Transitionen.
- Terminänderungen verwenden optimistic concurrency (`version`) und Datenbanktransaktionen.
- Aktive Fahrer- und Ressourcenreservierungen dürfen sich auf Datenbankebene nicht überschneiden.
- Fixierung, Statuswechsel und Outbox-Eintrag erfolgen atomar.
- E-Mail/SMS werden ausschließlich durch den Worker mit Idempotenz, Retry und sichtbarem Fehlerstatus versendet.
- Löschen historischer Kunden/Aufträge bedeutet standardmäßig Archivierung. Keine Historie durch Cascade Delete verlieren.
- Neue Provider immer hinter kleinen Interfaces implementieren und mit Fake-Adapter testen.

## Security

- Argon2id-Passworthashing mit parametrisierter Konfiguration.
- Opaque serverseitige Sessions; Cookie `Secure`, `HttpOnly`, angemessenes `SameSite`; Session-Rotation beim Login.
- CSRF-Schutz für alle zustandsändernden Browseranfragen.
- Autorisierung serverseitig an jedem Use Case, nicht nur durch versteckte Buttons.
- Confirmation- und Feed-Tokens mit kryptographisch sicherer Zufälligkeit erzeugen, nur gehasht speichern, widerrufbar machen und in Logs redigieren.
- Eingaben validieren, Ausgaben kontextgerecht escapen, CSP ohne `unsafe-eval`, keine Inline-Skripte ohne Nonce.
- Uploadgrößen, MIME-Typen, Timeouts, Rate Limits und Request Body Limits setzen.
- Ausgehende Webhook-/Provider-Ziele kommen nur aus validierter Startkonfiguration, nie aus Kundeneingaben.
- Keine Secrets im Repository, in Beispieldateien oder Tests.

## UI und Sprache

- UI-Texte auf Deutsch (`de-AT`), 24-Stunden-Zeit, m³ als Einheit.
- Auf Mobilgeräten Tagesansicht als Standard; Wochenansicht als mobile Wochenagenda oder responsive Liste.
- Drag-and-drop nicht als einzige Bedienmöglichkeit anbieten. Mobile Nutzer erhalten „Einplanen“ mit Datum/Uhrzeit-Dialog.
- Status nie ausschließlich durch Farbe kommunizieren; Text/Badge/Icon ergänzen.
- Touch-Ziele mindestens 44×44 CSS-Pixel, Formulare mit Labels und verständlichen Fehlern.
- Alle Fahrer sehen denselben Kalenderinhalt; Admin-Bearbeitungsfunktionen werden getrennt freigeschaltet.

## Qualitätsregeln

- Idiomatisches Go, kleine Packages, klare Fehlerwerte, `context.Context` an I/O-Grenzen.
- Keine globale mutable State für Geschäftslogik.
- `log/slog` mit strukturierten, redigierten Feldern.
- Uhr, Token-Generator, ID-Generator und Provider injizierbar machen.
- Unit-Tests für Domänenregeln; PostgreSQL-Integrationstests für Constraints/Transaktionen; Playwright für kritische Nutzerflüsse.
- Race Detector für Go-Tests, wo praktikabel.
- Keine neuen Abhängigkeiten ohne Begründung im Abschlussbericht; Versions- und Lockdateien aktualisieren.
- Generierte Dateien reproduzierbar erzeugen und mit `make generate-check` prüfen.

## Standardbefehle nach Bootstrap

```bash
make generate
make format
make lint
make test
make test-integration
make test-e2e
make build
make check
```

`make check` muss mindestens Generierung, Format, Lint, Unit- und Integrationstests abdecken. E2E kann in CI als eigener Job laufen.

## Definition of Done

- Task-Akzeptanzkriterien erfüllt.
- Migrationen vorwärts und rückwärts getestet, sofern das Migrationstool Down-Migrationen unterstützt.
- Berechtigungen und negative Fälle getestet.
- Fehlerzustände in der UI sichtbar und recoverable.
- Dokumentation, OpenAPI und Konfigurationsbeispiel aktualisiert.
- Keine geheimen Daten oder Roh-Tokens in Logs/Snapshots.
- Selbstreview des Diffs durchgeführt und im Abschlussbericht dokumentiert.

## Code Review Rules

Prüfe besonders:

- fehlende Admin-Gates und IDOR-Risiken;
- Race Conditions bei Terminverschiebung und doppelter Fixierung;
- Überschneidungen trotz Browserprüfung;
- Nebenwirkungen außerhalb der Transaktion;
- PII/Token-Leaks in Logs, URLs, Audit oder Fehlermeldungen;
- falsche Zeitzonen- oder DST-Konvertierung;
- automatische Speicherung aus Sprache/Planung;
- hardcodierte Einzelmaschine oder Einzeltransportmittel;
- fehlende mobile Alternative zu Drag-and-drop;
- fehlende Idempotenz bei Worker- und Kundenantworten.

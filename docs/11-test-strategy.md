# Teststrategie

## Ziel

Tests sichern nicht nur Happy Paths, sondern besonders Rechte, Zeit, Parallelität, Zustandswechsel und Nebenwirkungen.

## Testpyramide

### Unit-Tests

- Value Objects und Validierung;
- Statustransitionen;
- Dauerberechnung;
- Maps-Link-Builder;
- Bestätigungstoken-Hashing/Ablauf;
- Planungskandidatengenerierung und Score;
- Voice-Rule-Parser;
- Template-View-Modelle;
- Provider-Fakes und Retryentscheidungen.

### PostgreSQL-Integrationstests

- Migrationen auf leerer DB;
- `sqlc`-Queries;
- Exclusion Constraints für Fahrer/Ressourcen;
- optimistic concurrency;
- Fixierung + Reservierungen + Audit + Outbox atomar;
- Worker Claim mit `FOR UPDATE SKIP LOCKED`;
- Idempotenz von Bestätigung/Voice Commit;
- Feed-Token-Widerruf;
- Archivierungsregeln.

Integrationstests laufen gegen eine echte PostgreSQL-Version, nicht SQLite.

### HTTP-/Handler-Tests

- Auth, Sessionrotation, CSRF;
- Rollenmatrix;
- JSON-Fehlercodes;
- htmx Fragment vs. vollständige Seite;
- Uploadlimits;
- Security Header;
- Cache- und Referrer-Header auf öffentlichen Token-Seiten;
- redigierte Logs.

### Node-freie Browser-E2E mit Go/chromedp

Browserreisen werden als Go-Tests hinter dem Build-Tag `e2e` mit chromedp gegen eine lokal installierte Chrome-, Chromium- oder Edge-Version ausgeführt. Dadurch gibt es keinen Node-/npm-Abhängigkeitsbaum und keinen JavaScript-Buildschritt. `E2E_BROWSER_PATH` kann den Browserpfad explizit setzen; die Tests benötigen `TEST_DATABASE_URL` für eine echte PostgreSQL-18-Datenbank.

Kritische Flows:

1. Admin erstellt Kunde/Auftrag/Warteliste;
2. Admin zieht Auftrag in Kalender oder plant mobil;
3. Admin weist Fahrer/Ressource zu und fixiert;
4. Worker sendet über einen deterministischen Fake-SMTP-Adapter/Fake-SMS-Adapter;
5. Kunde bestätigt über Link;
6. Kalenderbadge wird grün;
7. Fahrer sieht Termin und Maps-Aktion;
8. Fahrer ergänzt Notiz und schließt Auftrag ab;
9. Fahrer kann Termin nicht verschieben/fixieren;
10. paralleler Admin-Tab erhält 409 und UI revertiert.

### Accessibility und visuelle Smoke-Tests

- axe oder gleichwertige Checks für zentrale Seiten;
- Viewports Smartphone, Tablet, Desktop;
- Keyboard-Navigation;
- Status nicht nur Farbe;
- Dialog-Fokus;
- reduzierte Bewegung.

## Zeit- und DST-Tests

Explizite Fälle für `Europe/Vienna`:

- Sommer-/Winterzeitdarstellung;
- DST-Sprung im März;
- doppelte Stunde im Oktober;
- ICS in UTC;
- Datumswunsch als lokales Datum;
- Termin über Mitternacht entweder abgelehnt oder korrekt unterstützt gemäß Businessregel.

## Parallelitätstests

- zwei Requests fixieren dieselbe Maschine im selben Slot;
- zwei Requests verschieben denselben Termin mit gleicher Version;
- Worker verarbeitet denselben Outbox-Eintrag parallel;
- Kunde klickt Bestätigen doppelt;
- Admin verschiebt Termin während Kunde antwortet;
- Feed wird widerrufen während Abruf läuft.

Mindestens ein konkurrierender DB-Test muss beweisen, dass nur eine Reservierung committen kann.

## Testdaten

`reference/seed-scenario.md` definiert reproduzierbare Kunden, Fahrer, Verfügbarkeit und Termine. Tests verwenden feste Uhr/IDs oder injizierbare Generatoren.

Keine echten Telefonnummern, E-Mails, API-Keys oder Kundennamen aus Produktion in Fixtures.

## Qualitätsbefehle

```bash
make generate-check
make format-check
make lint
make test
make test-race
make test-integration
make test-e2e
make security-scan
make build
```

`make check` bündelt alle schnellen, für jeden Task verpflichtenden Checks. Langsame E2E-/Scan-Jobs laufen zusätzlich in CI und vor Release.

`make test-e2e` startet keinen Browser- oder Datenbankdienst selbst. Vor dem Lauf müssen `TEST_DATABASE_URL` und ein kompatibler lokaler Chromium-Browser verfügbar sein; es wird weder Node noch npm installiert oder ausgeführt.

## Coverage

Kein blindes Prozentziel ersetzt Fachtests. Für Domänen- und Securitymodule wird hohe Branch-Abdeckung erwartet. Jeder behobene Fehler erhält einen Regressionstest.

## Testbericht im Codex-Abschluss

Codex berichtet:

- welche Befehle ausgeführt wurden;
- Anzahl/Status der Tests;
- nicht ausführbare Tests mit konkretem Grund;
- manuelle Checks;
- verbleibende Risiken.

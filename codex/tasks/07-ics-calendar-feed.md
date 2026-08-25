# Task 07 – ICS-Export und private Kalender-Abonnements

**Empfohlener Aufruf**

```text
$hackplan-implement Implementiere codex/tasks/07-ics-calendar-feed.md vollständig.
```

## Ziel

Interne Benutzer können HackWerk-Termine als standardkonforme ICS-Datei exportieren und einen privaten, widerrufbaren Kalenderfeed in Apple Kalender, Outlook, Google Kalender und anderen kompatiblen Clients abonnieren. Die Synchronisierung ist in V1 read-only.

## Vor der Implementierung lesen

- `AGENTS.md`, `PLANS.md`
- `docs/03-domain-model.md`
- `docs/04-status-state-machine.md`
- `docs/07-api-and-integrations.md`
- `docs/10-security-privacy.md`
- `docs/12-operations-deployment.md`
- `docs/14-configuration.md`
- `acceptance/ics.feature`

Erstelle `docs/exec-plans/07-ics-calendar-feed.md`.

## Scope

### Kalenderfeed-Datenmodell

Migrationen/Queries für `calendar_feeds`:

- Eigentümer-Benutzer;
- kryptographisch zufälliger Token, nur als Hash gespeichert;
- Feedname, Filter (z. B. alle Termine, ausgewählte Ressourcentypen, optional nur eigene Fahrerzuweisung als zusätzliche Option, aber Standard für Fahrer bleibt alle Termine);
- aktiv/widerrufen, erstellt/zuletzt verwendet;
- Tokenversion/Rotation;
- optional Ablaufdatum, standardmäßig kein kurzer Ablauf für Abonnements, dafür Widerruf/Rotation.

Raw Token wird nur einmal angezeigt. In UI und Logs danach nur maskiert.

### ICS-Generator

Implementiere einen kleinen testbaren Generator gemäß iCalendar:

- `VCALENDAR`, `VERSION:2.0`, `PRODID`, `CALSCALE:GREGORIAN`, sinnvoller Kalendername;
- pro relevanten Termin ein `VEVENT`;
- stabiler, nicht erratbarer `UID` auf Basis interner UUID + konfigurierter Domain, ohne Kundendaten;
- `DTSTAMP`, `DTSTART`, `DTEND`, `SEQUENCE`, `STATUS`, `LAST-MODIFIED`;
- UTC-Zeitwerte (`...Z`) bevorzugen, um komplexe VTIMEZONE-Fehler zu vermeiden; sichtbare Textangaben in lokaler Zeit;
- korrekte Text-Escapes, Line Folding und CRLF;
- `SUMMARY` mit datensparsamer, konfigurierbarer Darstellung;
- `DESCRIPTION` mit m³, Auftragstyp, Status, erlaubter interner Notizmenge und optionalem internen App-Link;
- `LOCATION` als Adresse; kein Confirmation-/Feed-Token in Eventdaten;
- `GEO` nur bei vorhandenen validen Koordinaten;
- cancelled Termine entweder mit gleicher UID + erhöhter `SEQUENCE` + `STATUS:CANCELLED` für definierten Zeitraum ausgeben oder konsistente dokumentierte Feedpolitik implementieren;
- abgeschlossene Termine bleiben innerhalb des Feed-Historienfensters sichtbar.

Änderungen an Zeit, Status oder relevanten Eventfeldern erhöhen `SEQUENCE` deterministisch. Ergänze gegebenenfalls ein persistiertes Feld oder einen berechenbaren revisionssicheren Mechanismus.

### Export und Feed-Endpunkte

1. Authentifizierter Export:
   - Datumsbereich und Filter serverseitig begrenzt;
   - `Content-Type: text/calendar; charset=utf-8` und Downloadname;
   - CSRF nicht für GET, aber Auth/RBAC und Rangevalidierung.

2. Privater Abonnementfeed:
   - öffentlicher GET nur mit unerratbarem Token;
   - konstante generische 404/410-Semantik bei ungültig/widerrufen;
   - `Cache-Control` und ETag/Last-Modified so wählen, dass Kalenderclients aktualisieren können, ohne sensible öffentliche Caches;
   - Token aus Accesslogs redigieren; Reverse-Proxy-Anleitung ergänzen;
   - Rate Limit mit Kalenderclient-tauglicher Toleranz;
   - keine Cookies/Sessionabhängigkeit.

### Benutzeroberfläche

Bereich „Kalender exportieren“ bzw. Einstellungen:

- einmaliger ICS-Export nach Zeitraum;
- Feed erstellen mit Name/Filter;
- nach Erzeugung vollständige URL genau einmal anzeigen und Copy-Button;
- klare Warnung: Link gewährt lesenden Zugriff, nicht weitergeben;
- Feedliste nur maskiert, zuletzt verwendet, rotieren und widerrufen;
- kurze Einrichtungsanleitungen für Apple Kalender, Outlook und Google Kalender ohne Garantie sofortiger Pollingintervalle;
- V1 deutlich als read-only markieren; Änderungen in externen Kalendern kommen nicht zurück.

### Privacy und Filter

- Definiere zwei Detailstufen: intern vollständig genug für Fahrer, optional „minimal“ für persönliche Geräte.
- Keine Telefonnummer, E-Mail, interne Notizen, Confirmationlinks oder technischen IDs standardmäßig.
- Feedfilter werden bei jeder Anfrage aus DB geladen; Rechte-/Deaktivierungsstatus des Besitzers wird geprüft. Deaktivierter Benutzer verliert Feedzugriff.
- Fahrerfeed darf standardmäßig alle Termine enthalten, wie Produktregel verlangt.

### Tests

- Unit-/Snapshot-Tests für Escaping, Umlaute, Komma/Semikolon/Zeilenumbruch, CRLF/Folding, UTC, Sequence, cancelled/completed und leeren Feed.
- Parser-Roundtrip mit einer unabhängigen ICS-Bibliothek nur im Test oder kleiner Validierung; Produktionsgenerator nicht unnötig abhängig machen.
- Integration: Tokenhash, Rotation, Widerruf, deaktivierter Nutzer, Filter und Range.
- E2E: Feed erzeugen, URL einmal sehen, abrufen, Termin verschieben, Sequence/Zeiten ändern, widerrufen.
- Security: Token nicht in Logs, keine PII im Minimalfeed, Rate-Limit, URL-Encoding.

## Verbindliche Regeln

- V1 ist einseitig/read-only; keine versteckte bidirektionale Sync-Logik.
- Feedtoken ist ein Secret und wird nur gehasht gespeichert.
- Kein Sessioncookie nötig oder gesetzt am Feed-Endpunkt.
- Stabile UID; Terminverschiebung darf nicht als völlig neues Event erscheinen.
- UTC/Zeitzone und RFC-Escaping werden automatisiert geprüft.

## Nicht Bestandteil

- OAuth zu Google/Microsoft;
- CalDAV-Server;
- Schreibzugriff aus externen Kalendern;
- garantierte sofortige Refreshintervalle externer Clients.

## Akzeptanzkriterien

- [ ] ICS-Export kann nach Zeitraum erzeugt werden und ist standardkonform parsebar.
- [ ] Private Feed-URL ist unerratbar, nur einmal vollständig sichtbar und widerrufbar/rotierbar.
- [ ] Terminänderungen behalten UID und erhöhen Sequence.
- [ ] Abgesagte Termine werden konsistent als Cancelled veröffentlicht.
- [ ] Deaktivierter Benutzer und widerrufener Feed verlieren Zugriff.
- [ ] Standardfeed enthält keine Telefonnummern, E-Mails, Tokens oder unnötige interne Notizen.
- [ ] Fahrer kann alle Termine abonnieren.
- [ ] UI erklärt Apple/Outlook/Google-Kompatibilität und read-only eindeutig.
- [ ] Accesslogs redigieren Feedtoken.
- [ ] `acceptance/ics.feature` ist automatisiert.

## Pflichtprüfungen

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

Validiere erzeugte Beispieldateien zusätzlich mit mindestens einem unabhängigen Parser und dokumentiere Ergebnis.

## Abschlussbericht

Beschreibe UID-/Sequence-Strategie, Zeitformat, Privacyfilter, Tokenrotation, Cachepolitik und getestete Clients/Parser. Stelle klar, dass V1 read-only ist.

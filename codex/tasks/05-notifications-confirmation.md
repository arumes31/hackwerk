# Task 05 – Transactional Outbox, E-Mail/SMS und sichere Kundenbestätigung

**Empfohlener Aufruf**

```text
$hackplan-implement Implementiere codex/tasks/05-notifications-confirmation.md vollständig.
```

## Ziel

Beim expliziten Fixieren eines Termins plant die Anwendung zuverlässig eine E-Mail und/oder SMS. Die Nachricht enthält einen sicheren Link, über den der Kunde ohne Konto bestätigen, ablehnen oder Rückruf wünschen kann. Versandfehler verlieren keine fachlichen Änderungen und sind für Admins sichtbar/retrybar.

## Vor der Implementierung lesen

- `AGENTS.md`, `PLANS.md`
- `docs/03-domain-model.md`
- `docs/04-status-state-machine.md`
- `docs/07-api-and-integrations.md`
- `docs/10-security-privacy.md`
- `docs/12-operations-deployment.md`
- `docs/14-configuration.md`
- `acceptance/confirmation.feature`
- vorhandene Kalender-/Outboximplementierung

Erstelle `docs/exec-plans/05-notifications-confirmation.md`.

## Scope

### Datenmodell

Migrationen/Queries für:

- vollständige `outbox_events` mit Typ, Payloadversion, Aggregat, Idempotenzschlüssel, Status, Versuchszähler, `available_at`, Claim/Lease, letztem redigiertem Fehler und Zeitstempeln;
- `notifications` pro Termin/Kanal mit Empfänger-Snapshot, Templateversion, Status, Provider-ID, Versuchen und minimierten fachlichen Parametern;
- `confirmation_requests` mit Hash eines mindestens 256-Bit-Tokens, Ablauf, Status, Antwort, Antwortzeit, `token_version`, Widerruf und Idempotenz;
- optional `notification_deliveries` nur falls Provider-Callbacks tatsächlich sauber modelliert werden; nicht vorsorglich überbauen.

Raw Tokens werden nie gespeichert. Ein Termin hat höchstens eine aktive Confirmation Request pro Tokenversion.

### Atomare Fixierung und Neuplanung

Vervollständige `FixAppointment`:

- validiert mindestens einen erreichbaren Kanal gemäß Kundenpräferenz oder verlangt einen expliziten Admin-Override „ohne Benachrichtigung“ mit Grund;
- setzt Termin `fixed`, Confirmation `pending` (oder dokumentiert `not_requested` beim Override), Job `scheduled`;
- erzeugt Confirmation Request und `notification.requested` Outboxevents in derselben DB-Transaktion;
- Roh-Token wird nur für den zu erzeugenden Link an den Outboxpayload-/sicheren Verschlüsselungsmechanismus übergeben. Bevorzuge ein Design, bei dem der Worker den nötigen Link sicher bilden kann, ohne Roh-Token dauerhaft ungeschützt in der DB zu speichern. Falls verschlüsselter Payload nötig ist, nutze einen separaten rotierbaren Schlüssel und dokumentiere Threat Model.

Beim Verschieben eines fixierten Termins:

- alte aktive Tokens widerrufen;
- vorherige Bestätigung ungültig;
- Confirmation wieder `pending`;
- neue Tokenversion und neue Nachricht atomar planen;
- alte Links zeigen verständlich „Termin wurde geändert/Link ungültig“.

### Worker und Outbox

- `hackwerk worker` pollt mit begrenzter Batchgröße, `FOR UPDATE SKIP LOCKED` oder äquivalenter sicherer Claimstrategie.
- Mehrere Worker dürfen parallel laufen, ohne doppelte fachliche Zustellung. Verwende Idempotenzschlüssel und Provider-ID, soweit möglich.
- Zustände `queued`, `sending/claimed`, `retry_wait`, `sent`, `failed/dead` gemäß Dokumentation.
- Exponentieller Backoff mit Jitter, Maxversuche und Dead-Letter-Sichtbarkeit.
- Crash nach Providererfolg vor DB-Commit ist explizit behandelt: nutze Provider-Idempotenzschlüssel, wo verfügbar; sonst dokumentiere unvermeidbares At-least-once-Risiko und formuliere Nachricht idempotent.
- Worker-Logs redigieren Empfänger und Token/Link; Metrics zählen Status/Versuche ohne PII.
- Admin kann fehlgeschlagene Benachrichtigung erneut einreihen, ohne doppelte aktive Tokens zu erzeugen.

### Provideradapter

Implementiere Ports und Adapter:

1. **Externes E-Mail-SMTP**
   - ausschließlich statisch konfigurierter externer Host; TLS/STARTTLS mit Zertifikatsprüfung, Auth aus Secret, Timeouts;
   - Text- und einfache HTML-Version;
   - From/Reply-To aus validierter Konfiguration;
   - kein lokaler Maildienst; Entwicklung und CI verwenden einen injizierten Fake-Adapter.

2. **SMS Webhook**
   - providerneutraler, serverseitig konfigurierter HTTPS-Endpunkt;
   - signierter Request (z. B. HMAC mit Zeitstempel), Idempotenzschlüssel, Timeouts, begrenzte Antwortgröße;
   - Ziel-URL niemals aus Benutzer-/Kundendaten;
   - Fake/Log-Adapter für Entwicklung und Tests.

3. **No-op/Fake**
   - deterministisch testbar, keine Rohinhalte in Logs.

### Nachrichtentemplates

Deutsche Templates enthalten mindestens:

- Kundennamen/Firma in angemessener Anrede;
- Wochentag, Datum, Startzeit `Europe/Vienna`;
- voraussichtliche Dauer und Holzmenge;
- Auftragstyp;
- sicheren Link;
- Hinweis, dass der Termin bestätigt/abgelehnt bzw. Rückruf angefordert werden kann;
- kontaktierbare Betriebsadresse/Telefon aus Konfiguration.

Templates sind versioniert und zentral testbar. Keine beliebige HTML-Eingabe aus Adminformularen.

### Öffentliche Confirmation Page

- Route mit Raw Token im Pfad oder Query; `Referrer-Policy: no-referrer`, `Cache-Control: no-store`, keine Drittanbieterassets/Analytics.
- GET zeigt nur minimale Termindaten und drei verständliche Aktionen:
  - Termin bestätigen
  - Termin ablehnen
  - Rückruf wünschen
- Zustandsänderung ausschließlich POST mit einem linkgebundenen Schutzmechanismus. Da kein Login existiert, verhindere Cross-Site-Automation durch SameSite/Origin soweit möglich und einen separaten Formularnonce bzw. Double-submit-artigen Flow, ohne den Link unbenutzbar zu machen.
- POST ist idempotent. Wiederholung zeigt dasselbe Resultat.
- Tokenvergleich constant-time; abgelaufen/widerrufen/ungültig geben generische, hilfreiche Seiten ohne Terminexistenz-Leak.
- `declined` und `callback_requested` lösen sichtbaren Adminhinweis/Outbox-internes Event aus, stornieren den Termin aber nicht automatisch.
- Keine Kunden-App und kein Kundenkonto erforderlich.

### Adminoberfläche

- Termindetail zeigt Confirmationstatus und Versandstatus je Kanal.
- Aktionen: erneut senden, Token widerrufen/neu erzeugen, Antwort administrativ zurücksetzen (mit Begründung), ohne Benachrichtigung fixieren nur mit Audit.
- Dashboard-ähnlicher Fehlerbereich für fehlgeschlagene/outstanding Nachrichten; vollständiges Dashboard folgt Task 06.
- Kontaktziel redigiert anzeigen, z. B. Teilmaskierung.

### Tests

- Unit: Template, Tokenhash/-ablauf, Transitionen, Backoff, Redaction.
- DB: atomare Fixierung+Outbox, Workerclaim mit mehreren Workern, Crash/retry, eindeutige Idempotenz, Tokenwiderruf bei Move.
- E2E: Admin fixiert; Fake-SMTP erhält Link; Kunde bestätigt/ablehnt/Rückruf über die sichere Webseite; Fahrer/Admin sieht Status; alter Link nach Move ungültig.
- Security: Token nicht in Logs/Audit/Referer, Rate Limits, ungültige Tokens, Replay/Idempotenz, XSS in Kundendaten, SSRF-Schutz des Webhooks.

## Verbindliche Regeln

- Kein SMTP/SMS direkt aus HTTP-Handler oder innerhalb der fachlichen Transaktion.
- Kein E-Mail-Empfang; Kundenantworten laufen ausschließlich über die sichere Confirmation-Seite.
- Fachliche Fixierung geht nicht verloren, wenn Provider nicht erreichbar ist.
- Kunde darf Termin nicht selbst verschieben/stornieren; nur Antwortstatus setzen.
- Ablehnung storniert Reservierung nicht automatisch.
- Keine SMS-Lesebestätigung als Confirmationquelle.
- Roh-Token, kompletter Link und Nachrichtentext nicht in Logs/Audit.

## Nicht Bestandteil

- eingehende SMS;
- WhatsApp/Push;
- Provider-spezifische Twilio/etc.-Bindung;
- automatische Terminfreigabe nach Ablehnung;
- bidirektionale externe Kalenderintegration.

## Akzeptanzkriterien

- [ ] Fixierung und Outbox/Confirmation sind atomar.
- [ ] E-Mail und konfigurierbarer SMS-Webhook funktionieren über denselben Providerport.
- [ ] Kunde kann ohne Konto bestätigen, ablehnen oder Rückruf wünschen.
- [ ] Antwort ist idempotent und im internen Kalender sichtbar.
- [ ] Ablehnung behält Terminreservierung bis Adminentscheidung.
- [ ] Verschieben eines fixierten Termins widerruft alten Link und erzeugt neue Anfrage.
- [ ] Worker ist parallel- und crashrobust, besitzt Retry/Dead-Letter-Sichtbarkeit.
- [ ] Admin kann Fehler sicher erneut senden.
- [ ] Tokens/PII erscheinen nicht in Logs, Audit, Referer oder Analytics.
- [ ] `acceptance/confirmation.feature` ist vollständig automatisiert.

## Pflichtprüfungen

```bash
make generate
make format
make lint
make test
make test-integration
make test-e2e
make test-race
make build
make check
```

Simuliere SMTP-/Webhook-Ausfall, Workercrash, parallele Worker, doppelten Kunden-POST und Terminverschiebung nach Bestätigung.

## Abschlussbericht

Beschreibe Outboxclaim-/Idempotenzstrategie, Tokenlebenszyklus, Providerkonfiguration, öffentliche Seitensicherheit, Retryverhalten und konkrete Redaction-Tests. Dokumentiere verbleibendes At-least-once-Risiko transparent, falls der Provider keine Idempotenz unterstützt.

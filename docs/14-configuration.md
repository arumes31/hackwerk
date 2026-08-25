# Konfigurationsmodell

## Prinzipien

- Startkonfiguration aus Environment/Docker Secrets;
- fachlich änderbare, nicht geheime Einstellungen in DB;
- Secrets nie über normale Admin-API zurückgeben;
- jede Konfiguration beim Start validieren;
- sichere Defaults in Development, fail-closed bei produktionskritischen Lücken.

## Server

- `APP_ENV=development|test|production`
- `APP_NAME=HackWerk`
- `APP_BASE_URL=https://...`
- `APP_LISTEN_ADDR=:18533`
- `APP_TIMEZONE=Europe/Vienna`
- `APP_LOCALE=de-AT`
- `APP_TRUSTED_PROXY_CIDRS=`
- `APP_SHUTDOWN_TIMEOUT=20s`

## Datenbank

- `DATABASE_URL_FILE=/run/secrets/database_url` bevorzugt;
- Pool Min/Max/Timeout;
- erwartete Schema-Version;
- separate Migration-URL optional.

## Auth/Session

- Session-Secret/Keyfile;
- Idle-/Absolute-Laufzeit;
- Cookie Name, Secure;
- Login Rate Limit;
- Argon2 Parameter;
- Passwort-Mindestlänge.

## E-Mail

- Enable;
- externer SMTP Host/Port/TLS-Modus ausschließlich für ausgehende Nachrichten;
- SMTP-Benutzername und Passwort aus Secrets;
- From Name/Address;
- Verbindungs-, Lese- und Schreibtimeouts;
- Max Attempts.

E-Mail-Empfang ist nicht Teil der Anwendung; konfiguriert wird ausschließlich der Versand.

## SMS Webhook

- Enable;
- statische HTTPS URL;
- HMAC Secret;
- Timeout;
- erlaubte Hostnamen/CIDRs;
- Max Attempts;
- Absenderkennung.

## Sprache/OpenAI optional

- `VOICE_ENABLED`;
- `VOICE_TRANSCRIBER=disabled|fake|openai`;
- `VOICE_EXTRACTOR=rules|openai`;
- API-Key aus Secret;
- konfigurierbare Modellnamen;
- maximale Dauer/Bytes;
- Draft Retention;
- externer Provider-Hinweistext.

## Routing/Geocoding

- Provider `haversine|http` oder konkrete spätere Adapter;
- statische Base URL aus Config;
- API-Key aus Secret;
- Timeout/Cache TTL;
- Depotlat/lon;
- Straßenfaktor und Durchschnittsgeschwindigkeit.

## Planung

DB-Settings mit Version:

- Betriebsintervalle je Wochentag;
- Slot-Raster Default 15 Minuten;
- Standardpuffer;
- Suchhorizont Default 90 Tage;
- Scoregewichte;
- Mindestfahrpuffer;
- maximale Kandidaten/Providercalls.

## Aufbewahrung

- Session Cleanup;
- Voice Draft 24h;
- Confirmation Token;
- Outbox/Notification Payload;
- Audit/Business Data;
- Backup Retention extern.

## Logging/Metriken

- Level/Format;
- Request Body Logging immer aus für sensible Routen;
- Metrics Bind/Protection;
- Trace Enable optional;
- Sampling ohne Token/PII.

Eine vollständige Vorlage steht in `reference/environment.example`.

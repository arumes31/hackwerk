# API- und Integrationsvertrag

## API-Stil

HackWerk ist primär serverseitig gerendert. JSON-Endpunkte existieren für FullCalendar, Sprache, Planung und Integrationen. Sie liegen unter `/api/v1` und werden in `openapi/openapi.yaml` dokumentiert.

HTML-Formulare und htmx-Endpunkte dürfen fachlich dieselben Application Services verwenden, nicht getrennte Logikpfade.

## Fehlerformat für JSON

```json
{
  "error": {
    "code": "appointment_version_conflict",
    "message": "Der Termin wurde zwischenzeitlich geändert.",
    "request_id": "...",
    "details": {
      "current_version": 7
    }
  }
}
```

- stabile maschinenlesbare Codes;
- deutsche Nutzertexte;
- keine Stacktraces oder Provider-Secrets;
- Feldfehler unter `details.fields`;
- `409` für Version/Konflikt, `422` für fachliche Validierung, `403` für Berechtigung, `404` ohne Existenz-Leak.

## Relevante Routen

### Auth/HTML

- `GET/POST /login`
- `POST /logout`
- `GET /`
- `GET /customers`, `/customers/{id}`
- `GET /jobs/{id}`
- `GET /waitlist`
- `GET /calendar`
- `GET /drivers`, `/availability`
- `GET /planning`
- `GET /settings`

### Kunden/Aufträge

- `POST /api/v1/customers`
- `PATCH /api/v1/customers/{id}` mit `version`
- `POST /api/v1/customers/{id}/archive`
- `POST /api/v1/jobs`
- `PATCH /api/v1/jobs/{id}` mit `version`
- `POST /api/v1/jobs/{id}/waitlist`
- `DELETE /api/v1/jobs/{id}/waitlist`
- `POST /api/v1/jobs/{id}/notes`

### Kalender

- `GET /api/v1/calendar?from=<iso>&to=<iso>` (maximal 93 Tage)
- `GET /api/v1/calendar/conflicts?from=<iso>&to=<iso>&driver_id=...&resource_id=...`
- `POST /api/v1/calendar/plan` erzeugt Draft, Zuweisungen und Proposal, aber nie einen fixierten Termin;
- `POST /api/v1/appointments/{id}/assign`
- `POST /api/v1/appointments/{id}/propose`
- `POST /api/v1/appointments/{id}/move`
- `POST /api/v1/appointments/{id}/resize`
- `POST /api/v1/appointments/{id}/fix`
- `POST /api/v1/appointments/{id}/cancel`
- `POST /api/v1/appointments/{id}/complete`
- `GET /api/v1/appointments/{id}`

Browsermutationen verwenden wegen CSRF und serverseitigem Form-Limit `application/x-www-form-urlencoded`; JSON-Antworten folgen dem gemeinsamen Fehlerformat. Move-Beispiel:

```text
starts_at=2026-09-01T06%3A00%3A00Z&ends_at=2026-09-01T09%3A00%3A00Z&version=6&csrf_token=...
```

Antwort enthält aktuelle Version und normalisiertes Calendar Event.

### Fahrer/Verfügbarkeit

- `GET /api/v1/drivers`
- `POST/PATCH /api/v1/drivers/...` Admin
- `GET /api/v1/me/availability`
- `PUT /api/v1/me/availability/rules`
- `POST /api/v1/me/availability/exceptions`
- Admin-Varianten für fremde Fahrer unter `/api/v1/drivers/{id}/availability/...`.

### Planung

- `POST /api/v1/planning/suggestions`
- `POST /api/v1/planning/suggestions/{id}/adopt`

Der Request enthält Job-ID, optionalen Suchzeitraum und gewünschte Ressourcen. Die Antwort enthält Kandidaten, Scorekomponenten, Warnungen und Inputversionen.

### Sprache

- `POST /api/v1/voice/transcriptions` multipart, begrenzt;
- `POST /api/v1/voice/drafts/{id}/extract`;
- `PATCH /api/v1/voice/drafts/{id}`;
- `POST /api/v1/voice/drafts/{id}/commit`;
- `DELETE /api/v1/voice/drafts/{id}`.

Commit erzeugt Kunde/Auftrag/Warteliste atomar und ist idempotent über `Idempotency-Key`.

### Öffentliche Kundenantwort

- `GET /termin/{token}`
- `POST /termin/{token}/antwort`

POST Body enthält `action=confirm|decline|callback` plus Formnonce. Die Seite lädt keine externen Assets, sendet keine Referrer und wird nicht gecacht.

### ICS

- `GET /calendar/export.ics?from=&to=` angemeldet;
- `POST /api/v1/calendar-feeds` erzeugt Roh-Token genau einmal;
- `DELETE /api/v1/calendar-feeds/{id}` widerruft;
- `GET /feeds/{token}/calendar.ics` öffentlich als Capability URL.

Der Zugriffspfad muss in Access Logs redigiert werden. Antwort unterstützt `ETag` und `Last-Modified`.

### Betrieb

- `GET /health/live`
- `GET /health/ready`
- `GET /metrics` nur intern oder per Schutzmiddleware.

## FullCalendar Event Payload

```json
{
  "id": "appointment-uuid",
  "title": "Franz Huber · HW-2026-000001",
  "start": "2026-09-01T06:00:00Z",
  "end": "2026-09-01T09:00:00Z",
  "editable": true,
  "className": "calendar-event--fixed calendar-confirmation--pending",
  "color": "#28659b",
  "contrastColor": "#ffffff",
  "extendedProps": {
    "job_id": "...",
    "version": 7,
    "lifecycle": "fixed",
    "confirmation": "pending",
    "job_type": "chipping_with_transport",
    "volume_m3": "80.00",
    "customer_name": "Franz Huber",
    "locality": "Unterneukirchen",
    "version": 7,
    "can_fix": false,
    "can_cancel": true
  }
}
```

Für Fahrer setzt der Server `editable=false`, unabhängig von Browserparametern. Der allgemeine Eventfeed enthält keine Telefonnummern oder E-Mail-Adressen; Kontakte gehören ausschließlich in eine berechtigte Detailansicht.

## Google Maps

Der serverseitig generierte Link verwendet eine standardisierte Maps-URL mit `api=1` und entweder `destination=lat,lon` oder URL-kodierter Adresse. Beliebige vom Nutzer gespeicherte Redirect-URLs werden nicht geöffnet.

## E-Mail

Ausgehende E-Mail verwendet ausschließlich einen externen SMTP-Dienst. Das Ziel stammt aus validierter Startkonfiguration; Zugangsdaten kommen aus Secrets. TLS-Zertifikate werden validiert, Klartextmodi sind in Produktion verboten und Timeouts/Antwortgrößen sind begrenzt. E-Mail-Empfang ist nicht vorgesehen; Kundenantworten erfolgen über die sichere Confirmation-Seite. Templates sind versioniert und werden mit Test-Snapshots geprüft; lokale und CI-Tests verwenden einen Fake-SMTP-Adapter statt eines Maildienst-Containers.

## SMS Webhook

- URL ausschließlich aus Startkonfiguration;
- HTTPS in Produktion;
- JSON Body mit Zielnummer, Text, Message-ID und Idempotency-Key;
- HMAC-Signatur mit Zeitstempel;
- Timeout, begrenzte Antwortgröße und kein Redirect auf private Netze;
- Providerfehler redigieren;
- Development-Adapter schreibt nur maskierte Zielnummer und Message-ID.

## Routing und Geocoding

Externe Adapter sind optional. Die manuelle App bleibt ohne Provider funktionsfähig. Planung kennzeichnet Fallback-Entfernungen ausdrücklich.

## OpenAI als optionaler Sprachprovider

API-Key nur serverseitig. Audio wird über den offiziellen Transkriptionsendpunkt verarbeitet. Modellname ist konfigurierbar. Extraktion verwendet ein striktes JSON-Schema und speichert niemals frei erfundene Werte ohne Konfidenz/Quelle.

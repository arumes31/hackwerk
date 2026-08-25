# Sicherheits- und Datenschutzanforderungen

## Schutzgüter

- Kundenkontakt- und Standortdaten;
- Auftrags- und Terminplanung;
- Fahrer-Verfügbarkeit und Abwesenheiten;
- Benutzerkonten und Sessions;
- Bestätigungs- und Kalenderfeed-Tokens;
- SMS-/SMTP-/Speech-/Routing-Secrets;
- Audit- und Backupdaten.

## Bedrohungsübersicht

| Risiko | Gegenmaßnahmen |
|---|---|
| Fahrer nutzt Admin-API | serverseitige RBAC an jedem Use Case, negative Tests |
| doppelte Maschinenbuchung | Exclusion Constraint, Transaktion, optimistic concurrency |
| CSRF verschiebt Termin | synchronizer token, SameSite als Zusatz, POST/PATCH only |
| gestohlene Session | opaque serverseitig, Rotation, Secure/HttpOnly, Revocation |
| Link erraten/geleakt | 256-Bit Token, Hash at rest, Ablauf, Widerruf, no-referrer |
| Feed-URL in Logs | Pfadredaktion, Tokenhash, widerrufbar, keine Analytics-Third-Parties |
| PII in Logs | Allowlist strukturierter Felder, Maskierung, keine Request Bodies |
| Provider-SSRF | Endpoint nur aus Startup-Config, Schema/Hostprüfung, Redirects einschränken |
| Audio-Upload-Missbrauch | Auth, Limits, MIME, tmpfs, Timeout, Rate Limit |
| XSS über Bemerkung | templ escaping, keine untrusted HTML, CSP |
| Brute Force | Login-Rate-Limit, progressive delay, Audit, generische Fehler |
| verlorene Nachricht | Transactional Outbox, Retry, Dead Letter, Dashboard |
| manipuliertes Audit | append-only DB-Rechte, keine Edit/Delete-Routen, Backup |

## Authentifizierung

- Benutzername und Passwort, keine Selbstregistrierung;
- Passwörter mit Argon2id, individuellem Salt und versionierten Parametern;
- Mindestlänge und Blockierung offensichtlich kompromittierter/zu schwacher Passwörter, ohne komplizierte Zeichenklassen zu erzwingen;
- Admin kann temporäres Passwort setzen, Benutzer muss es ändern;
- Session-ID kryptographisch zufällig, nur Hash/opaque Serverzustand;
- Sessionrotation nach Login und Passwortwechsel;
- deaktivierte Benutzer verlieren aktive Sessions;
- optionaler TOTP/WebAuthn-Ausbau später, Architektur nicht blockieren.

## Session-Cookie

- `Secure` in Produktion;
- `HttpOnly`;
- `SameSite=Lax` oder `Strict` abhängig vom Login-/Linkflow;
- `Path=/`, keine unnötige Domain;
- Idle- und absolute Laufzeit konfigurierbar;
- Logout serverseitig widerruft;
- keine Session in LocalStorage.

## CSRF

Jeder authentifizierte zustandsändernde Browserrequest besitzt einen geheimen, sitzungsgebundenen CSRF-Token. htmx/Fetch senden ihn als Header; klassische Formulare als Hidden Field. Fehlende/falsche Tokens werden abgelehnt und sicher geloggt.

Die öffentliche Kundenantwort ist ein Capability-Flow. Sie verwendet GET nur zur Darstellung und POST mit zusätzlicher Formnonce. Der Token selbst erscheint nie in externen Assets oder Referrer-Headern.

## Autorisierung und IDOR

- Berechtigungen im Application Service;
- Queries scopen Feed/User/Driver-Objekte;
- keine Mass-Assignment-Updates aus beliebigem JSON;
- Patch-DTOs mit expliziten Feldern;
- 404/403-Strategie ohne unnötige Existenzinformation;
- Admin-Override benötigt Grund und Audit.

## Tokenregeln

- 32 zufällige Bytes oder mehr;
- URL-safe Base64 ohne Semantik;
- nur SHA-256/HMAC-Hash speichern;
- konstante Vergleichsfunktion;
- Ablauf spätestens zum Termin plus konfigurierbare Grace Period;
- neue Terminzeit widerruft alte Bestätigungstokens;
- Roh-Token genau einmal anzeigen/versenden;
- Access Logs und Traces redigieren.

## Security Header

Mindestens:

- `Content-Security-Policy` mit `default-src 'self'`, kein `unsafe-eval`;
- `frame-ancestors 'none'`;
- `X-Content-Type-Options: nosniff`;
- `Referrer-Policy: no-referrer` auf Token-Seiten, sonst restriktiv;
- `Permissions-Policy` nur Mikrofon auf Same-Origin-Seite;
- HSTS durch Reverse Proxy nach korrekter HTTPS-Einrichtung;
- `Cache-Control: no-store` für Login, Tokenantwort und sensible Formulare.

## Eingabe und Ausgabe

- serverseitige Validierung aller DTOs;
- Mengen-/Dauer-/Datumsgrenzen;
- Unicode normalisieren, aber Originalnamen bewahren;
- Telefon normalisieren ohne Originalverlust;
- keine vom Nutzer gelieferte HTML-Ausgabe;
- CSV-Export später gegen Formula Injection schützen;
- Providerantworten als untrusted input behandeln.

## Logging und Audit

Technische Logs enthalten Request-ID, Route-Template, Status, Dauer, User-ID und Fehlercode. Nicht enthalten:

- Passwort/Hash;
- Session-, CSRF-, Confirmation- oder Feed-Token;
- vollständige Telefonnummer/E-Mail;
- kompletter Nachrichtentext;
- Audio oder Transkript;
- ungefilterte Providerantworten.

Audit enthält fachliche Aktionen und Changed-Field-Namen, nicht vollständige PII-Snapshots. Zugriff auf Audit ist Admin-only.

## Secrets

- Docker Secrets oder sichere Environment-Injection;
- keine Secrets in `.env.example`;
- Startvalidierung mit klarer Fehlermeldung ohne Secretwert;
- Providersecrets nicht über Admin-UI auslesen;
- Rotation dokumentieren;
- getrennte Development-/Production-Werte.

## Datenaufbewahrung

Konfigurierbar und dokumentiert:

- uncommitted Voice Drafts 24 Stunden;
- temporäres Audio sofort löschen;
- Sessions nach Ablauf bereinigen;
- alte Outbox-Payloads minimieren/archivieren;
- Audit und Auftrags-/Terminhistorie gemäß betrieblichem Bedarf;
- Backups verschlüsselt und mit Löschzyklus;
- archivierte Kunden bleiben für Historie, brauchen aber Admin-Prozess für endgültige Löschung/Anonymisierung.

Dies ist eine technische Datenschutzbasis, keine automatische rechtliche Freigabe. Produktive Einführung benötigt dokumentierte Verantwortlichkeiten, Datenschutzhinweise, Providerverträge und einen abgestimmten Aufbewahrungsplan.

## Dependency- und Supply-Chain-Sicherheit

- Go-Abhängigkeiten minimal und fixiert; kein Node-/npm-Abhängigkeitsbaum;
- `go mod verify`, `govulncheck`, Prüfsummen für eingecheckte Browserbibliotheken und Container-Scan in CI;
- keine dynamischen CDN-Skripte;
- Base Images auf unterstützte Major-/Patchversionen aktualisieren;
- SBOM im Release erzeugen;
- generierte Artefakte reproduzierbar bauen.

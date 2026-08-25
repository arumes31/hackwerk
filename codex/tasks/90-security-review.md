# Review 90 – Security-, Privacy- und Abuse-Case-Review

**Empfohlener Aufruf**

```text
$hackplan-review Prüfe das gesamte Repository gemäß codex/tasks/90-security-review.md. Behebe bestätigte Blocker/High Findings mit Regressionstests.
```

## Ziel

Führe einen unabhängigen, evidenzbasierten Security- und Datenschutzreview durch. Suche nicht nur nach Bibliothekslücken, sondern nach fehlerhaften Vertrauensgrenzen in Auth, RBAC, öffentlichen Tokens, Provideradaptern, Uploads, Logs, Exporten und Betriebsconfig.

## Vor dem Review lesen

- `AGENTS.md`
- `docs/03-domain-model.md`
- `docs/04-status-state-machine.md`
- `docs/05-rbac.md`
- `docs/07-api-and-integrations.md`
- `docs/09-voice-intake.md`
- `docs/10-security-privacy.md`
- `docs/12-operations-deployment.md`
- `docs/14-configuration.md`
- relevante ADRs und alle öffentlichen HTTP-Routen

## Arbeitsweise

1. Erfasse Angriffsflächen und Datenflüsse.
2. Prüfe Code, Migrationen, Konfiguration und Container.
3. Reproduziere plausible Defekte mit Tests/Requests, statt nur statisch zu spekulieren.
4. Klassifiziere `Blocker`, `High`, `Medium`, `Low` und nenne CWE/OWASP-Kategorie nur wenn passend.
5. Belege jedes Finding mit Datei/Zeile, Angriffsvoraussetzung, Auswirkung, Reproduktion und konkreter Korrektur.
6. Behebe bestätigte Blocker/High Findings innerhalb des Scopes mit Regressionstests. Ändere Produktregeln nicht stillschweigend.
7. Erzeuge `docs/reviews/90-security-review.md` und aktualisiere bei Fixes relevante Doku.

## Prüfkatalog

### Authentifizierung und Sessions

- Argon2id-Parameter, Passwortpolicy, Rehash, temporäre Passwörter;
- Login User Enumeration, Brute Force, Rate Limit Bypass, Proxy-IP-Vertrauen;
- Sessiontoken-Entropie, nur gehasht gespeichert, Rotation, Fixation, Logout/Revocation;
- Idle/absolute Expiry, deaktivierte Benutzer, letzter Admin;
- Cookieflags, Scope, SameSite, Secure hinter Proxy, Session in URL/Logs;
- Open Redirects nach Login.

### Autorisierung und IDOR

Erstelle eine Endpunkt-/Use-Case-Matrix und teste mindestens:

- Fahrer ruft Admin-User-/Ressourcen-/Planungsaktionen direkt auf;
- Fahrer verschiebt/fixiert/storniert/ändert Dauer via JSON/Formrequest;
- Nutzer greift auf fremden Voice Draft, Feed, Notification oder interne Abwesenheitsnotiz zu;
- manipulierte Customer/Job/Appointment IDs;
- archivierte/inaktive Objekte;
- Mass Assignment von `role`, `status`, `fixed_by`, `version`, `user_id`;
- fehlender Service-Level-Gate trotz geschützter UI.

### CSRF, XSS und Browsergrenzen

- alle mutierenden Cookie-Endpunkte inklusive htmx, JSON und Logout;
- Origin/Host/Content-Type-Prüfung;
- gespeicherte/reflektierte XSS in Name, Firma, Ort, Notiz, Transkript, Providerfehler, Kalender und ICS;
- templ/HTML-Attribute/URLs/JSON-in-Script-Kontexte;
- CSP ohne unnötiges `unsafe-inline`/`unsafe-eval`, Nonce-Handling;
- Clickjacking, MIME Sniffing, Referrer, Drittanbieterrequests;
- CSV-/ICS-Formel-/Control-Character-Injection.

### Öffentliche Confirmation- und Kalenderfeed-Tokens

- Entropie, Hash, constant-time compare, Rotation, Ablauf/Widerruf;
- Tokenleak in Logs, Accesslogs, Referer, Analytics, Browsercache, Fehlertracking, Audit;
- Replay/Idempotenz und Zustandswechsel über alten Link;
- Brute Force und Rate Limits ohne Kunden-Existenzorakel;
- Cross-Site-Automation der Confirmation POSTs;
- Feedzugriff nach Benutzerdeaktivierung/Widerruf;
- sensible PII im ICS und Cache-Header;
- Token in URL ist als Secret in Reverse-Proxy-Doku berücksichtigt.

### Provider, SSRF und Supply Chain

- SMS-/Routing-/Speech-/Webhook-Ziele ausschließlich aus validierter Konfiguration;
- Redirects, DNS-Rebinding-Risiko, private IPs falls externe URL frei konfigurierbar, TLS-Verifikation;
- Timeouts, Response-/Body-Limits, Context-Cancel, Retry-Amplifikation;
- HMAC-/Timestamp-/Idempotenzdesign des SMS-Webhooks;
- API Keys in Headern, Logs, Errors, Dockerlayern;
- externe SMTP-STARTTLS-/TLS-Modi, Zertifikatsprüfung, Größenlimits und Secret-Behandlung;
- Go-/Node-Abhängigkeiten, Lockfiles, `govulncheck`, OSV, Image/SBOM;
- Runtime-CDN oder dynamische Scripts.

### Upload und Voice

- Auth/CSRF/RBAC, Größen-/Zeit-/Concurrent Limits;
- MIME/Containerprüfung, Path Traversal, Symlink, Dateiname, Temp-Rechte;
- Transcoding/Subprocess ohne Shellinjection, Ressourcenerschöpfung/Decompression Bomb;
- Audio öffentlich erreichbar, Retention/Cleanup, Backup-Ausschluss;
- Transkript/Audio in Logs, Traces, Crashreports, Providerpayloads;
- anderer Benutzer liest/committet Draft;
- direkter Commit ohne Review/Validierung;
- Voice kann einen Termin/status manipulieren.

### Datenbank, Secrets und Logs

- SQL-Injection und unsichere dynamische Sortierung/Filter;
- DB-Rolle/Privileges, Migrationuser vs Appuser;
- Klartext-Tokens/Sessions/Passwörter;
- PII in Audit, outbox payload, notifications, logs, metrics, health;
- Backups mit richtigen Rechten/Secrets/Retention;
- `*_FILE`, Environment-Dumps, Debugendpunkte;
- Restore/Seed in Produktion.

### Container und Deployment

- non-root, read-only, capabilities, Docker socket, exposed Postgres/Metrics;
- unsichere Devdefaults im Produktionsmodus;
- Trusted Proxy/Host Header, HTTP→HTTPS-Annahmen, HSTS;
- Healthresponse mit Secrets/Versionsrisiken;
- Admin-/Metricsports;
- Images/Actions gepinnt, Build-Secrets, `.dockerignore`.

## Abuse Cases, die explizit getestet werden

- Ein Fahrer versucht einen Termin durch manuell geänderten Request zu fixieren.
- Ein Kunde klickt denselben Confirmationlink mehrfach und danach einen alten Link nach Verschiebung.
- Ein Angreifer enumeriert Confirmation-/Feedtokens.
- Kundennotiz enthält HTML/JavaScript und landet in Kalender, E-Mail und ICS.
- SMS-/Routingprovider liefert riesige, langsame oder umleitende Antwort.
- Voiceupload ist zu groß/falsch/parallel und versucht Pfad-/Shellinjection.
- Reverse Proxy setzt gefälschte `X-Forwarded-For`-/Proto-Header aus untrusted Quelle.
- Fehlerlog wird mit Telefonnummer, E-Mail und Token-Canaries provoziert.

## Erwartete Test-/Toolnutzung

- vorhandene Unit-/Integration-/Playwrighttests;
- gezielte `curl`-/HTTP-Tests gegen lokale Compose-Umgebung;
- `go test -race`;
- `govulncheck`, OSV/Node Audit, Container-/Secret-Scan;
- Logcapture und Suche nach Canarywerten;
- keine destruktiven Tests gegen produktive Systeme.

## Findingformat

```markdown
### [HIGH] Kurzer Titel

- **Ort:** `path/file.go:123`
- **Voraussetzung:** …
- **Auswirkung:** …
- **Nachweis/Reproduktion:** …
- **Ursache:** …
- **Fix:** …
- **Regressionstest:** …
- **Status:** open/fixed/accepted-with-expiry
```

## Abschlusskriterien

- [ ] Endpunkt-/Datenfluss-/Rollenmatrix erstellt.
- [ ] Alle Prüfkategorien evidenzbasiert bearbeitet.
- [ ] Blocker/High bestätigt und behoben oder klar als Releaseblocker markiert.
- [ ] Regressionstests für Fixes vorhanden.
- [ ] Keine Roh-PII/Tokens im Reviewbericht.
- [ ] Scanresultate bewertet, nicht nur angehängt.
- [ ] Releaseempfehlung `GO`, `NO-GO` oder `GO WITH CONDITIONS` mit Begründung.

## Abschlussbericht

Bericht in `docs/reviews/90-security-review.md`, zusätzlich kurze Zusammenfassung im Codex-Resultat: positive Kontrollen, Findings nach Schwere, behobene Punkte, verbleibende Blocker und ausgeführte Tests/Scans.

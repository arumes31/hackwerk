# Task 01 – Authentifizierung, Benutzerverwaltung, Sessions, RBAC und Auditbasis

**Empfohlener Aufruf**

```text
$hackplan-implement Implementiere codex/tasks/01-foundation-auth-rbac.md vollständig.
```

## Ziel

Administratoren und Fahrer können sich sicher anmelden. Rollen werden serverseitig durchgesetzt. Ein Administrator kann die zunächst sechs Benutzer verwalten und Fahrerprofile zuordnen. Sensible Aktionen werden in einem minimierten Audit-Trail erfasst.

## Vor der Implementierung lesen

- `AGENTS.md`, `PLANS.md`
- `docs/03-domain-model.md`
- `docs/05-rbac.md`
- `docs/06-ux-and-responsive.md`
- `docs/10-security-privacy.md`
- `docs/14-configuration.md`
- `reference/permissions-matrix.csv`
- `acceptance/permissions.feature`
- vorhandenen Task-00-Code und Abschlussbericht

Erstelle `docs/exec-plans/01-foundation-auth-rbac.md`.

## Scope

### Datenbank

Erzeuge Migrationen und Queries für:

- `users` gemäß Domänenmodell;
- `drivers` als eigenständiges Profil mit optionalem `user_id`;
- opaque serverseitige `sessions` mit gehashtem Sessiontoken, Ablauf, absolutem Ablauf, Rotation/Revocation, letzter Nutzung und minimierten Metadaten;
- `audit_events` mit Actor, Aktion, Objektart/-ID, Request-ID und redigierten Metadaten;
- notwendige Indizes, Case-insensitive Username-Eindeutigkeit und Constraints;
- `version`/Zeitstempel dort, wo spätere konkurrierende Adminänderungen möglich sind.

Sessions werden aus der DB entfernt oder zuverlässig als widerrufen markiert. Kein Klartext-Sessiontoken in der DB.

### Authentifizierung

- Loginseite und `POST /login` mit generischer Fehlermeldung.
- Argon2id-Passworthashing mit zentraler, konfigurierbarer Parametrisierung und Rehash-on-login bei veralteten Parametern.
- Sessionrotation bei Login; Logout widerruft serverseitig.
- Cookie in Produktion `Secure`, immer `HttpOnly`, geeigneter `SameSite`-Wert, enger Path, keine Domain ohne Bedarf.
- Idle- und absolute Session-Laufzeit; Ablauf serverseitig geprüft.
- Rate Limiting für Login und öffentliche Auth-Endpunkte, ohne Benutzerexistenz zu verraten.
- Optionales `must_change_password`: erzwungener Passwortwechsel vor Zugriff auf Fachseiten.
- Keine Passwortzurücksetzung per E-Mail in V1. Admin kann ein temporäres Passwort setzen; Nutzer muss es ändern.

### Autorisierung/RBAC

- Definiere typisierte Rollen/Permissions, keine verstreuten Stringvergleiche.
- Middleware darf Navigation/HTTP-Zugriff schützen; jeder mutierende Application Service prüft die Berechtigung zusätzlich.
- Standardmäßig deny.
- Admin-only: Benutzer anlegen/bearbeiten/deaktivieren, Rollen ändern, Fahrerprofile verbinden, Passwörter zurücksetzen.
- Fahrer dürfen keine Admin-Endpunkte durch direkte Requests verwenden.
- Deaktivierung eines Nutzers widerruft aktive Sessions atomar oder unmittelbar wirksam.
- Der letzte aktive Admin darf nicht versehentlich deaktiviert oder zum Fahrer herabgestuft werden.

### CSRF und Browsersecurity

- CSRF-Token für alle cookie-authentifizierten zustandsändernden Browserrequests einschließlich htmx.
- Origin/Host-Prüfungen entsprechend Sicherheitsdokument.
- Baseline Security Headers und CSP mit lokalen Assets; keine `unsafe-eval`.
- Einheitliche sichere Fehlerbehandlung; keine Stacktraces/SQL-Details im Browser.

### Benutzeroberfläche

- Login, Logout, Passwort ändern, Profilanzeige.
- Adminbereich „Benutzer & Fahrer“ mit Liste, Status, Rolle, Fahrerzuordnung, Erstellen/Bearbeiten/Deaktivieren und temporärem Passwort.
- Responsive Navigation entsprechend Rolle; noch leere Fachmodule als klar gekennzeichnete, nicht irreführende Einträge oder deaktiviert.
- Formfehler bleiben an Feldern, eingegebene nicht-sensible Werte bleiben erhalten.
- Keine Passwortwerte im DOM nach Antwort; keine Autofill-Blockade.

### CLI und Seed

- Implementiere `hackwerk admin create`, `reset-password`, `list` mit sicherer Passwortübergabe via interaktivem Prompt oder stdin/file; kein Passwort als Prozessargument erzwingen.
- `seed-dev` erzeugt deterministische Demo-Accounts nur bei expliziter Dev-Konfiguration. Dokumentiere Zugangsdaten in lokaler Ausgabe/README, nicht in produktiver Config.
- Seed insgesamt sechs Benutzer: mindestens ein Admin und mehrere Fahrer, passend zu `reference/seed-scenario.md`.

### Tests und Dokumentation

- Unit-Tests für Passwortpolicy, Rollen und Sessionablauf.
- PostgreSQL-Integrationstests für Benutzer-Eindeutigkeit, Sessionrotation/-revocation, letzten Admin und Audit.
- Handler-/E2E-Tests für Login, Logout, CSRF, erzwungenen Passwortwechsel, Adminverwaltung und direkte Fahrerzugriffe.
- Teste konstante/generische Fehlermeldungen; keine User Enumeration durch sichtbare Texte.
- OpenAPI/HTTP-Dokumentation für relevante Form-/JSON-Endpunkte aktualisieren.

## Verbindliche Regeln

- Keine öffentliche Registrierung.
- Passwort- und Session-Rohwerte niemals loggen oder auditieren.
- Audit speichert Changed-Field-Namen, nicht vollständige Benutzer-/Kontaktdaten.
- Deaktivierte Benutzer verlieren sofort Zugriff.
- UI-Verstecken ersetzt nie Autorisierung.
- Fahrerprofile bleiben getrennt von Nutzerkonten; ein Fahrer kann ohne Login existieren.

## Nicht Bestandteil

- Kunden, Aufträge, Warteliste und Kalender;
- MFA/Passkeys/SSO (als späteres Backlog dokumentieren);
- Passwortreset per öffentlichem E-Mail-Link.

## Akzeptanzkriterien

- [ ] Admin und Fahrer können sich anmelden/abmelden; inaktive Benutzer nicht.
- [ ] Passwort wird mit Argon2id gehasht und bei Bedarf transparent neu gehasht.
- [ ] Sessiontoken liegt nur gehasht in PostgreSQL und wird beim Login rotiert.
- [ ] CSRF blockiert fehlende/ungültige Tokens auch bei htmx-Requests.
- [ ] Fahrer erhält bei direktem Admin-Request 403 beziehungsweise eine sichere UI-Antwort.
- [ ] Admin kann Nutzer/Fahrerprofile verwalten; letzter aktiver Admin ist geschützt.
- [ ] Passwortreset erzwingt `must_change_password` und widerruft alte Sessions.
- [ ] Alle kritischen Adminaktionen erzeugen minimierte Audit-Events.
- [ ] Navigation und Seiten sind auf Smartphone und Desktop bedienbar.
- [ ] Die Szenarien aus `acceptance/permissions.feature` für diesen Scope sind automatisiert.

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

Führe zusätzlich negative HTTP-Tests für CSRF, IDOR, deaktivierte Sessions und Rollenmanipulation aus.

## Abschlussbericht

Beschreibe Sessionmodell, Cookiepolicy, Argon2id-Parameterquelle, Berechtigungsdurchsetzung, Auditdaten und Testnachweise. Nenne bewusst noch nicht implementierte Auth-Erweiterungen als Backlog, nicht als TODO im Code.

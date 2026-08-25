# ExecPlan 01 – Authentifizierung, RBAC und Audit

Status: Implementierung und lokale Gates abgeschlossen; PostgreSQL-/Docker-Laufzeitnachweis ausstehend wegen Registry-Proxy-Reset

## Ziel und Sicherheitsgrenzen

Task 01 ergänzt das Bootstrap-Fundament um lokale Benutzer, getrennte Fahrerprofile, Argon2id-Passwörter, opaque DB-Sessions, CSRF, typisierte Berechtigungen und minimiertes Audit. Jeder mutierende Use Case prüft die Rolle zusätzlich zur HTTP-Middleware. Der letzte aktive Administrator bleibt geschützt; Deaktivierung und Passwortreset widerrufen Sessions atomar.

## Umsetzung

1. Migration und sqlc-Vertrag für Benutzer, Fahrer, Sessions, Login-Limits und Audit.
2. Reine Auth-Domäne für Rollen, Permissions, Passwortpolicy und Tokenhashing.
3. PostgreSQL-Adapter mit Transaktionen für Loginrotation und Adminmutationen.
4. Application Service und CLI für Create/List/Reset/Seed.
5. HTTP-Middleware, CSRF-/Origin-Prüfung und responsive deutsche Seiten.
6. Unit-, Handler- und PostgreSQL-Integrationstests; OpenAPI und Betriebsdoku.

## Entscheidungen

- Session- und CSRF-Rohwerte werden jeweils mit 256 Bit CSPRNG erzeugt; PostgreSQL speichert nur SHA-256-Hashes.
- Das CSRF-Rohmaterial liegt in einem eng begrenzten `SameSite=Strict`-Cookie und muss zusätzlich zum Formular-/htmx-Header mit dem DB-Hash übereinstimmen. Origin und Host werden separat geprüft.
- Login-Limit-Schlüssel sind gehasht; weder eingegebene Nutzernamen noch IP-Adressen werden in technische Logs geschrieben.
- Argon2id-Parameter kommen aus typisierter Startkonfiguration und werden im PHC-String versioniert; erfolgreiche Anmeldung rehasht veraltete Parameter.

## Nachweise

- `go test ./...`: grün, einschließlich Argon2id, Rehash, generischer Fehler, Rate Limit, Ablauf, CSRF, Cookiepolicy und direktem Fahrer/Admin-Negativtest.
- `go tool golangci-lint run ./...`: 0 Befunde.
- `go test -tags=integration ./tests/integration/...`: Tests kompilieren; Ausführung verlangt `TEST_DATABASE_URL` und wird mit Compose nachgeholt.
- Docker-/PostgreSQL-Smoke-Tests werden erneut ausgeführt, sobald der lokale Registry-Proxy stabile Image-Pulls zulässt.

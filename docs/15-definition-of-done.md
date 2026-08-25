# Definition of Done

## Pro Task

- Taskziel und alle Akzeptanzkriterien erfüllt;
- Code kompiliert und ist formatiert;
- neue/angepasste Migrationen und Queries vorhanden;
- Unit- und notwendige Integrationstests vorhanden;
- negative Rollen-/Validierungsfälle getestet;
- Dokumentation und OpenAPI aktualisiert;
- keine TODO-/Mock-Platzhalter im vereinbarten Scope;
- keine neuen Lint-/Vulnerability-Fehler;
- Diff-Selbstreview dokumentiert;
- `make check` erfolgreich.

## Für UI-Features

- Desktop, Tablet und Smartphone geprüft;
- Tastatur- und Touchalternative;
- Fehler/Loading/Empty State;
- Status nicht nur Farbe;
- htmx-Fallback oder verständliche Fehlermeldung;
- Browser-E2E für kritischen Flow.

## Für Datenbankänderungen

- Constraint und Index begründet;
- Migration auf leerer und bestehender Test-DB;
- Backfill/Default bedacht;
- parallele Zugriffe getestet, wenn terminrelevant;
- kein Verlust historischer Daten;
- Rollbackstrategie dokumentiert.

## Für Provider/Nebenwirkungen

- Interface + Fake;
- Timeout, Retry, Idempotenz;
- redigierte Fehler;
- Outbox statt Versand im Request;
- Ausfall sichtbar, Kernworkflow bleibt kontrolliert;
- Secrets nur serverseitig.

## Release 1.0

Zusätzlich:

- Tasks 00–11 abgeschlossen;
- Reviews 90–93 ohne offene Blocker;
- alle Gherkin-Kernflüsse automatisiert oder klar begründet;
- Backup und Restore erfolgreich getestet;
- Production Compose/Deployment-Dokumentation geprüft;
- Admin-CLI getestet;
- Seed/Smoke-Test erfolgreich;
- Container non-root, Healthchecks grün;
- SBOM und Vulnerability Scan erzeugt;
- Datenschutzhinweis/Providerkonfiguration betrieblich geklärt;
- bekannte Einschränkungen in Release Notes.

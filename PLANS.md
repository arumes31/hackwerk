# Ausführungspläne für HackWerk

Für Aufgaben, die mehrere Module, Migrationen oder mehr als einen klaren Arbeitsschritt betreffen, erstellt Codex vor der Implementierung eine Datei unter `docs/exec-plans/<task-id>-<slug>.md`.

Ein Ausführungsplan ist ein lebendes Dokument. Fortschritt, entdeckte Risiken und abweichende Entscheidungen werden während der Arbeit aktualisiert.

## Vorlage

```markdown
# ExecPlan: <Titel>

## Ziel und sichtbares Ergebnis

Beschreibe in wenigen Sätzen, welches Verhalten nach Abschluss für Benutzer oder Betrieb sichtbar ist.

## Kontext und betroffene Bereiche

- relevante Task-Datei
- relevante Produkt-/Architekturdokumente
- betroffene Packages, Tabellen, Endpunkte und UI-Seiten

## Annahmen und feste Entscheidungen

Liste nur Annahmen, die nicht bereits in `docs/13-decisions-and-assumptions.md` festgelegt sind. Verändere keine verbindliche Produktregel stillschweigend.

## Risiken

- Datenmigration
- Berechtigung
- Nebenwirkungen/Outbox
- Zeitzone/DST
- Parallelität
- mobile Bedienung
- Datenschutz

## Umsetzungsschritte

1. Schritt mit konkretem Ergebnis
2. Schritt mit konkretem Ergebnis
3. ...

Jeder Schritt soll klein genug sein, dass sein Ergebnis getestet und bei Bedarf zurückgesetzt werden kann.

## Datenbankänderungen

Tabellen, Constraints, Indizes, Backfill, Rollback und Kompatibilität beschreiben.

## Testplan

- Unit-Tests
- Integrationstests
- Browser-/E2E-Tests
- manuelle Smoke-Checks
- negative Berechtigungsfälle

## Fortschritt

- [ ] Schritt 1
- [ ] Schritt 2
- [ ] Schritt 3

## Entdeckungen und Entscheidungen während der Umsetzung

Datum, Erkenntnis und Begründung dokumentieren.

## Abschlussnachweis

- ausgeführte Befehle und Resultate
- relevante Screens/Flows
- bekannte Restpunkte außerhalb des Scopes
- Diff-Selbstreview
```

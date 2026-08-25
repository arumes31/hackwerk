# Task XX – <Titel>

**Empfohlener Aufruf**

```text
$hackplan-implement Implementiere codex/tasks/XX-slug.md vollständig.
```

## Ziel

Beschreibe das sichtbare Ergebnis aus Benutzersicht.

## Vor der Implementierung lesen

- `AGENTS.md`
- `PLANS.md`
- relevante Produkt-/Architekturdokumente
- vorherige Task-Abschlussberichte und vorhandenen Code

Erstelle oder aktualisiere bei mehreren Modulen `docs/exec-plans/XX-slug.md`.

## Ausgangslage

Beschreibe, was aus den vorigen Tasks vorhanden sein muss.

## Scope

### Datenbank und Domain

- …

### Application/HTTP

- …

### UI

- …

### Betrieb/Tests/Dokumentation

- …

## Verbindliche Regeln

- …

## Nicht Bestandteil

- …

## Akzeptanzkriterien

- [ ] …

## Pflichtprüfungen

```bash
make generate
make format
make lint
make test
make test-integration
make build
make check
```

Ergänze je nach Scope `make test-e2e`, `make test-race`, `make scan`.

## Abschlussbericht

Nenne geänderte Bereiche, Migrationen, Sicherheits-/Berechtigungsentscheidungen, ausgeführte Tests und verbleibende Punkte außerhalb des Scopes. Prüfe den eigenen Diff gegen jedes Akzeptanzkriterium.

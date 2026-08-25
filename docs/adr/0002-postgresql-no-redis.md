# ADR 0002: PostgreSQL als einziges persistentes System

## Status
Angenommen.

## Entscheidung
Sessions, Outbox, Terminlocks, Audit und Geschäftsdaten liegen in PostgreSQL. Kein Redis in Release 1.0.

## Begründung
Weniger Betriebsaufwand, starke Constraints und Transaktionen. Die erwartete Last ist klein. Prozesslokale Caches sind nur Optimierung und nie Source of Truth.

## Folgen
DB-Pool und Cleanup müssen sauber dimensioniert sein. Ein Redis darf später nur mit neuer ADR für ein konkretes Problem ergänzt werden.

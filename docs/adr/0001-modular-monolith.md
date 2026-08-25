# ADR 0001: Modularer Monolith

## Status
Angenommen.

## Kontext
Die erste Installation hat sechs Benutzer und eine Hackmaschine, benötigt aber konsistente Transaktionen über Auftrag, Termin, Ressourcen und Benachrichtigung.

## Entscheidung
Ein Go-Modul, ein PostgreSQL-Schema, ein Image, getrennte `serve`- und `worker`-Prozesse. Domänenmodule besitzen klare Packages/Ports.

## Folgen
Einfaches Deployment und atomare Geschäftsprozesse. Spätere Extraktion bleibt möglich. Package-Grenzen und Architekturtests müssen Kopplung begrenzen.

# ADR 0004: Transactional Outbox

## Status
Angenommen.

## Entscheidung
Fixierung und andere benachrichtigungsrelevante Statusänderungen schreiben in derselben DB-Transaktion ein Outbox-Ereignis. Ein Worker versendet mit Retry und Idempotenz.

## Begründung
Kein verlorener Versand zwischen DB-Commit und Provideraufruf, kurze HTTP-Requests, sichtbare Fehler.

## Folgen
Worker, Claiming, Retention und Dead-Letter-UI sind Teil des Produkts.

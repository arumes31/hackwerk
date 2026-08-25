# ADR 0005: Erklärbare regelbasierte Planung

## Status
Angenommen.

## Entscheidung
Version 1.0 verwendet deterministische Kandidatengenerierung und gewichtetes Scoring, keine autonome ML-Planung.

## Begründung
Kleine Datenbasis, hoher Bedarf an Nachvollziehbarkeit, Admin bleibt verantwortlich.

## Folgen
Scores, Warnungen und Inputversionen werden angezeigt. Provider/Fallback sind austauschbar. Spätere Optimierer müssen denselben Sicherheitsvertrag einhalten.

---
name: hackplan-review
description: Prüfe HackPlan-Code oder einen Diff gegen Produktregeln, Security, RBAC, Terminparallelität, Zeitzone, mobile UX und Tests. Verwenden für Review-Tasks 90–93 oder PR-Selbstreviews; nicht primär zum Implementieren neuer Features.
---

1. Lies `AGENTS.md`, den angegebenen Review-Task und relevante Spezifikationen.
2. Bestimme Review-Scope und Base/Head-Diff. Führe Tests oder gezielte Reproduktionen aus, wo möglich.
3. Priorisiere Findings nach `Blocker`, `High`, `Medium`, `Low`.
4. Belege jedes Finding mit Datei/Zeile, konkretem Risiko und reproduzierbarem Szenario.
5. Prüfe besonders Admin-only-Fixierung, IDOR, CSRF, Token/PII-Leaks, Exclusion Constraints, optimistic concurrency, Outbox-Atomarität, DST, mobile Alternativen und Auto-Save-Risiken.
6. Melde keine spekulativen Stilprobleme als Securityfinding. Unterscheide bewiesenen Defekt, wahrscheinliches Risiko und Verbesserung.
7. Wenn der Auftrag Fixes einschließt, behebe kleine klar abgegrenzte Findings mit Regressionstests; große Architekturänderungen als separaten Task vorschlagen.
8. Schließe mit positiver Abdeckung, offenen Blockern und einer klaren Release-Empfehlung.

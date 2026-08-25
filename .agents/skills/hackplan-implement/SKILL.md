---
name: hackplan-implement
description: Implementiere einen HackPlan-Task aus codex/tasks mit Planung, Migrationen, Tests, Dokumentation und Diff-Selbstreview. Verwenden, wenn eine konkrete HackPlan-Funktion gebaut oder geändert werden soll; nicht für reine Reviews.
---

1. Lies `AGENTS.md`, die angegebene Task-Datei und alle dort genannten Dokumente vollständig.
2. Inspiziere den bestehenden Code und bestehende Konventionen, bevor du Dateien änderst. Erfinde keine parallele Architektur.
3. Bei mehreren Modulen/Migrationen erstelle zuerst einen ExecPlan nach `PLANS.md` und halte ihn aktuell.
4. Implementiere eine vollständige vertikale Scheibe: Datenmodell, Application Service, Adapter, Handler, UI, Audit, Tests und Dokumentation, soweit die Task dies verlangt.
5. Halte unverhandelbare Regeln ein: Admin-only-Fixierung, alle Fahrer sehen alle Termine, keine Auto-Fixierung durch Planung/Sprache, generische Ressourcen, Europe/Vienna, serverseitige Autorisierung.
6. Prüfe Nebenwirkungen und Parallelität. Nutze DB-Transaktionen, Versionen und Outbox statt Browservertrauen oder Fire-and-forget.
7. Füge keine neue Abhängigkeit hinzu, ohne zunächst Standardbibliothek/bestehende Pakete zu prüfen. Begründe jede neue Abhängigkeit im Abschluss.
8. Führe Generierung, Format, relevante Tests und `make check` aus. Behebe Fehler innerhalb des Task-Scopes.
9. Vergleiche den finalen Diff Zeile für Zeile mit den Akzeptanzkriterien und den Code Review Rules in `AGENTS.md`.
10. Antworte mit: Ergebnis, wesentliche Dateien/Migrationen, Testbefehle und Resultate, Sicherheits-/Datenentscheidungen, bekannte Restpunkte außerhalb des Scopes.

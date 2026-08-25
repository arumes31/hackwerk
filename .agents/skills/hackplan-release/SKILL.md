---
name: hackplan-release
description: Erstelle und verifiziere einen HackPlan Release Candidate einschließlich Migration, Build, E2E, Security Scan, Backup/Restore, SBOM und Release Notes. Nur für Release- und Produktionsbereitschaftsaufgaben verwenden.
---

1. Lies `AGENTS.md`, `docs/12-operations-deployment.md`, `docs/15-definition-of-done.md` und den Release-Task.
2. Prüfe Repositoryzustand, Versionierung, Migrationen und Changelog.
3. Führe den vollständigen Check-, E2E-, Race-, Vulnerability- und Container-Build-Pfad aus.
4. Teste Migration auf leerer DB und Upgrade von der letzten unterstützten Version.
5. Führe einen echten Backup-/Restore-Smoke-Test in isolierter Testumgebung durch.
6. Verifiziere non-root, Healthchecks, Read-only-Betrieb, Secret-Injection und Logredaktion.
7. Erzeuge SBOM, Checksums und Release Notes ohne Secrets.
8. Gib `GO`, `NO-GO` oder `GO WITH CONDITIONS` mit konkreten Blockern und Nachweisen aus.

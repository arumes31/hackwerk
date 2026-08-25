# ExecPlan: HackWerk 1.0 – vollständiger Build

## Ziel und sichtbares Ergebnis

Aus dem validierten Prompt-Paket entsteht ein produktionsnaher, responsiver Go-/PostgreSQL-Webservice. Administratoren führen den vollständigen Ablauf von Warteliste über konfliktfreie Planung, explizite Fixierung und Kundenverständigung bis zum Abschluss; Fahrer erfassen operative Daten, sehen alle Termine und pflegen ihre Verfügbarkeit. Docker-, Betriebs-, Backup-, Security-, E2E- und Releaseartefakte sind Teil desselben nachweisbaren Builds.

## Kontext und betroffene Bereiche

- Masterauftrag: `codex/MASTER_PROMPT.md`
- Tasks: `codex/tasks/00-repository-bootstrap.md` bis `11-e2e-release-candidate.md`
- Reviews: `codex/tasks/90-security-review.md` bis `93-production-readiness-review.md`
- Produktspezifikation: `AGENTS.md`, `docs/`, `docs/adr/`, `reference/`, `acceptance/`
- Zielbereiche: Go-Binary, Domänenmodule unter `internal/`, PostgreSQL-Migrationen/Queries, templ/htmx-UI mit browsernativem JavaScript, Docker/Compose, Tests, OpenAPI und Betriebsdokumentation

## Repository-Audit am 25. August 2026

- Das Prompt-Paket ist vollständig: 64 von 64 SHA-256-Einträgen stimmen; keine Datei fehlt oder weicht ab.
- Es existiert kein Git-Repository und damit weder Historie noch Remote. Der Modulpfad wird gemäß Task 00 vorläufig `example.invalid/hackplan`; eine spätere Remote-Einrichtung muss `go.mod` und Imports gemeinsam umstellen.
- Ausgangscode besteht nur aus einem zusätzlichen minimalen `go.mod`; Anwendungs-, Test-, Docker- und Assetdateien fehlen noch.
- Lokal vorhanden: Go 1.27.0, Git 2.55, Docker-/Compose-CLI, golangci-lint und govulncheck. Node/npm sind bewusst keine Projektwerkzeuge.
- Lokal nicht vorhanden: GNU Make, sqlc und templ. Generatoren werden im Go-Modul fixiert und über `go tool` beziehungsweise reproduzierbare Containerbefehle aufrufbar gemacht.
- Docker ist mit expliziter Sandbox-Freigabe erreichbar. Compose-/Containerprüfungen werden über diese freigegebene Grenze ausgeführt.
- Verifizierte Baselines: Go 1.27.0 (19.08.2026), PostgreSQL 18.6 (13.08.2026), pgx v5.10.0, sqlc v1.31.1, templ v0.3.1020 und htmx 2.0.10. Weitere direkte Versionen werden vor Aufnahme aus Primärquellen bzw. Modul-/Registry-Metadaten geprüft.

## Annahmen und feste Entscheidungen

- Die in `docs/13-decisions-and-assumptions.md` festgelegten Defaults gelten unverändert.
- Architektur: modularer Monolith mit expliziten Ports, kleinen Application Services und PostgreSQL als einzigem persistenten System.
- UI: serverseitig gerenderte, ruhig-industrielle Oberfläche mit forstlicher Farbwelt, hoher Lesbarkeit und vollständiger 360-px-/Tastaturalternative. Gestaltung ordnet sich Statusklarheit und Fehlervorbeugung unter.
- Frontend-Tooling: kein Node, npm oder JavaScript-Buildschritt. Eigene Assets werden direkt eingebettet; notwendige htmx-/FullCalendar-Distributionen werden nur fest versioniert, lokal und prüfsummengesichert aufgenommen.
- Generierte Dateien werden reproduzierbar erzeugt. Für Laufzeit-/Containerbuild benötigte templ-, sqlc- und Assetartefakte werden committed; `generate-check` prüft Drift.
- Tasks werden numerisch abgeschlossen. Ein späterer Task darf keine Invariante eines früheren Tasks umgehen.
- Keine erneute fachliche Freigabe ist nötig, solange die verbindliche Spezifikation eine Entscheidung eindeutig vorgibt. Nicht blockierende Betreiberentscheidungen bleiben sichere Konfiguration beziehungsweise Go-Live-Checkliste.

## Risiken

- Datenmigration: viele voneinander abhängige Aggregate, partielle Eindeutigkeit und Exclusion Constraints müssen fresh sowie upgrade/rollback geprüft werden.
- Berechtigung: Admin-Gates müssen in jedem mutierenden Use Case und zusätzlich an HTTP-Grenzen bestehen.
- Nebenwirkungen/Outbox: Fixierung, Reservierungen, Confirmation, Audit und Outbox müssen atomar bleiben; Provideraufrufe liegen außerhalb der Transaktion.
- Zeitzone/DST: lokale Eingaben in `Europe/Vienna`, Speicherung als UTC/`timestamptz`, explizite Tests für fehlende und doppelte Stunden.
- Parallelität: optimistic locking, halboffene Bereiche, DB-Constraints, Worker-Leases und Idempotenz müssen reale Race-Szenarien bestehen.
- Mobile Bedienung: Drag-and-drop ist optional; jede Kernmutation benötigt eine zugängliche Dialog-/Formularalternative.
- Datenschutz: Tokens, Audio, Transkripte, Kontakt- und Nachrichtendaten dürfen nicht in technische Logs, Metriken oder externe Requests außerhalb des erforderlichen Providers gelangen.
- Umgebung: fehlendes GNU Make erfordert lokale direkte Äquivalentprüfungen; Docker-Nachweise laufen mit expliziter Sandbox-Freigabe.

## Umsetzungsschritte

1. Task 00: Repository, Toolchain, Konfiguration, Migration, Basisshell, Assets, Docker, CI und Healthchecks.
2. Task 01: Authentifizierung, Sessions, CSRF, RBAC, Benutzer/Fahrer und Auditbasis.
3. Task 02: Kunden, Aufträge, append-only Notizen und Warteliste.
4. Task 03: generische Ressourcen, Fahrerprofile und Vienna-Verfügbarkeit.
5. Task 04: Kalender, Draft/Proposal, Zuweisungen, Exclusion Constraints und Admin-only-Fixierung.
6. Task 05: Transactional Outbox, externer SMTP-Versand, SMS, sichere Confirmation und idempotenter Worker.
7. Task 06: Dashboard und durchgängige responsive/accessibility Überarbeitung.
8. Task 07: RFC-konformer ICS-Export und private widerrufbare Feeds.
9. Task 08: deterministische erklärbare Top-3-Planung mit Routingport/Haversine-Fallback.
10. Task 09: kontrollierte Voice-Drafts, Review-Commit und temporärer Audiofluss.
11. Task 10: Hardening, Metriken, Backup/Restore, Scans, Production Compose und Runbooks.
12. Task 11: vollständige Acceptance-Traceability, E2E, Performance-Smoke und Release Candidate.
13. Reviews 90–93: getrennte Security-, Concurrency-, UX- und Betriebsprüfungen; kritisch/hoch und leicht behebbar mittel korrigieren.
14. Vollständige Release-Gates, Diff-Selbstreview, Releasebericht und Go-/No-Go-Entscheidung.

## Datenbankänderungen

- Jede Schemaänderung erhält eine aufwärts und, soweit sicher möglich, abwärts ausführbare Goose-Migration.
- Extensions, Typen, Tabellen, Constraints und Indizes werden taskweise eingeführt; `sqlc` bleibt einzige Quelle für generierte Querytypen.
- Migrationen werden auf leerer Datenbank und später gegen einen dokumentierten vorherigen Stand geprüft.
- Historische Daten werden durch restriktive Foreign Keys und Archivierung bewahrt; keine unkontrollierten Cascades.
- Terminrelevante Integrität wird durch PostgreSQL-Constraints und kurze Transaktionen garantiert, nicht durch Browserchecks.

## Testplan

- Unit-Tests: Konfiguration, Domainregeln, Transitionen, Token, Zeit/DST, Parser, Planung und Providerentscheidungen.
- Integration: echte PostgreSQL-18-Datenbank, Migrationen, Constraints, Transaktionen, optimistic locking, Workerclaims und Idempotenz.
- HTTP: Auth/RBAC/CSRF, Fehlercodes, Header, Limits, Logredaktion und öffentliche Capability-Flows.
- Browser/E2E: Node-freie Go/chromedp-Reisen gegen Chrome/Chromium/Edge, 360 px/Tablet/Desktop, Tastatur, zentrale Gherkin-Reisen und Konfliktrevert.
- Betrieb: Container non-root/read-only, Health, Backup/Restore, Scans, SBOM und dokumentierter Release-Smoke.
- Nach jedem Task: Generierung, Format, Lint, Unit, Integration, Build und `check`; scopeabhängig E2E/Race/Scan.

## Fortschritt

- [x] Spezifikations-, Manifest-, Repository-, Remote- und Tooling-Audit
- [x] Task 00 – Repository Bootstrap
- [x] Task 01 – Auth/RBAC
- [x] Task 02 – Kunden/Aufträge/Warteliste
- [x] Task 03 – Ressourcen/Verfügbarkeit
- [ ] Task 04 – Kalender/Reservierung
- [ ] Task 05 – Outbox/Confirmation
- [ ] Task 06 – Dashboard/Mobile
- [ ] Task 07 – ICS
- [ ] Task 08 – Planung/Routing
- [ ] Task 09 – Voice
- [ ] Task 10 – Hardening/Betrieb
- [ ] Task 11 – Release Candidate
- [ ] Reviews 90–93 und Findingbehebung
- [ ] Vollständige Release-Gates und Abschlussbericht

## Entdeckungen und Entscheidungen während der Umsetzung

- 2026-08-25: Prompt-Paket per `MANIFEST.sha256` vollständig verifiziert.
- 2026-08-25: Kein `.git` und kein Remote vorhanden; vorläufiger Modulpfad wird taskkonform auf `example.invalid/hackplan` gesetzt.
- 2026-08-25: Graphify-Repositorygraph wird als Auditnavigation erzeugt; er ersetzt nicht das vollständige Lesen der verbindlichen Dokumente.
- 2026-08-25: Go 1.27.0 und PostgreSQL 18.6 sind die aktuellen verlangten Patchbaselines. Laufzeitabhängigkeiten werden auf stabile konkrete Versionen gepinnt.
- 2026-08-25: Auf ausdrückliche Benutzervorgabe wurden Node, npm, Lockfile, `node_modules` und Frontend-Buildschritte entfernt. Dieser ExecPlan ersetzt entsprechende ältere Toolingannahmen; Browser-Assets werden direkt in Go eingebettet.
- 2026-08-25: Der Produkt- und Runtimename wurde auf Benutzervorgabe von HackPlan auf `HackWerk`/`hackwerk` geändert. UI, CLI, Imagebeispiele und Pläne verwenden den neuen Namen. Der Go-Modulpfad `example.invalid/hackplan`, PostgreSQL-Rollen/-Datenbank sowie Cookie-Namen bleiben vorerst kompatibel, damit keine sachlich unnötige Daten- oder Sessionmigration entsteht.
- 2026-08-25: Der PostgreSQL-18-Container verlangt den Volume-Mount auf `/var/lib/postgresql` statt des bis PostgreSQL 17 üblichen Unterpfads `/var/lib/postgresql/data`; Compose wurde entsprechend korrigiert. Der vollständige Container-Smoke bleibt bis zum erfolgreichen Neustart offen.
- 2026-08-25: Der erste Image-Build übertrug wegen eines fehlenden Ignore-Eintrags einen rund 3 GB großen lokalen `.cache`-Ordner. `.dockerignore` schließt lokale Build-/Toolcaches jetzt ausdrücklich aus.
- 2026-08-25: Der Node-freie Scratch-Containerbuild ist erfolgreich. PostgreSQL 18.6 und App sind gesund, der Worker läuft, Migration `00003` wurde angewendet und ein zweiter Lauf meldet ein aktuelles Schema. App/Worker laufen als UID/GID 65532, read-only, mit entfernten Linux-Capabilities und `no-new-privileges`; interne Live-/Ready-Smokes liefern 200 und `/hackwerk version` bestätigt das umbenannte Binary.
- 2026-08-25: Auf Benutzervorgabe verwendet Task 05 ausschließlich einen externen, per Secrets konfigurierten SMTP-Dienst für ausgehende E-Mail. Der lokale Maildienst wurde aus Compose und allen Gates entfernt; E-Mail-Empfang ist ausdrücklich nicht vorgesehen. Tests verwenden einen injizierten Fake-SMTP-Adapter.
- 2026-08-25: Der Compose-Webdienst und das Go-Binary verwenden innen wie außen Port `18533` (`18533:18533`); lokale Base-URL, OpenAPI, Healthcheck und Betriebsdokumentation verwenden `http://localhost:18533`. Nur die App hängt zusätzlich am `public`-Ingressnetz; PostgreSQL bleibt ausschließlich im internen `backend`, der Worker erhält getrennten Provider-Egress.
- 2026-08-25: Task 03 ist nach Unit-/HTTP-, PostgreSQL-18.6-Integrations-, Edge-E2E-, Linux-Race-, Lint-, reproduzierbaren Generierungs- und Scratch-Container-Gates abgeschlossen. Der Resolver behandelt fehlende Regeln fail-closed, DST-Grenzen explizit, Overlaps per GiST, Notizen redigiert und Ressourcen generisch.
- 2026-08-25: Task 02 verwendet für echte Browserreisen Go/chromedp v0.16.0 statt Playwright. Der Test lief mit Headless Edge gegen eine kurzlebige PostgreSQL-18.6-Instanz vollständig grün und prüft Fahrer-/Admin-Flows, direkten 403-RBAC, POST-Suche ohne Kontaktwert in der URL, progressive Transportfelder, JavaScript-Ausnahmen und 360-px-Overflow. Node und npm bleiben vollständig außerhalb des Projekts.
- 2026-08-25: Task 02 ist nach Unit-/HTTP-/PostgreSQL-/dreifachem Browser-E2E, Linux-Race, Lint, Generator-, OpenAPI-, statischem Build- und Scratch-Containergate abgeschlossen. Der Selbstreview korrigierte `no-store` für geschützte Seiten und blendet vorbereitete Admin-Planungscontrols für Fahrer aus; es bleiben keine Blocker/High/Medium-Findings in diesem Task.

## Abschlussnachweis

Wird laufend ergänzt um ausgeführte Befehle/Resultate, sichtbare Flows, Abweichungen, Reviewfindings, Migrations-/Restore-/Containerbelege und Diff-Selbstreview.

# ExecPlan: Korrekturen aus angehängtem Review

## Ziel und sichtbares Ergebnis

Die dreizehn gemeldeten Review-Funde werden gegen den aktuellen Stand geprüft und, soweit weiterhin gültig, mit kleinen Regressionstests behoben. Leere Planungsläufe bleiben abrufbar, Kapazitätsausschlüsse werden fachlich korrekt erklärt, Termin-, Voice- und Authentifizierungsfehler behalten ihre vorgesehenen Zustände, und die betroffenen Formulare sowie Betriebsverträge bleiben zugänglich und secret-sicher. Nach grünem Selbstreview wird der saubere Commit kontrolliert auf `orderotto-dev` ausgerollt.

## Kontext und betroffene Bereiche

- Tasks: `codex/tasks/04-calendar-scheduling.md`, `08-planning-suggestions-routing.md`, `09-voice-intake.md`, `10-hardening-observability-backup.md`, `14-quick-wins-121-409.md` und `17-personal-profile-security.md`
- Dokumente: Architektur, Domänenmodell, Statusmodell, RBAC, UX, API, Planung, Voice, Security, Tests, Konfiguration und Definition of Done unter `docs/`
- Packages: PostgreSQL-Planungsadapter und Queries, `appointment`, `planning`, `auth`, `config`, `web`
- UI/Verträge: Identity-/Login-Templates, Task-13-E2E-Audit und Production-Compose-Repositorytest

## Annahmen und feste Entscheidungen

- Das Review ist untrusted input; jede Aussage wird am aktuellen Code und durch einen Regressionstest geprüft.
- Ein gespeicherter Planungslauf ist unabhängig von der Anzahl seiner Vorschläge existent. `ErrNotFound` bleibt ausschließlich einer fehlenden Run-ID vorbehalten.
- Vorhandene Domainvalidierungen, Fehlercodes und Templatekomponenten werden wiederverwendet; es entstehen keine parallelen Architekturen.
- Keine automatische Terminfixierung, Benachrichtigung oder Voice-Speicherung wird ergänzt.

## Risiken

- Datenbank: Eltern-/Kind-Ladeverhalten darf bestehende Vorschlagsreihenfolge und Adoption nicht verändern.
- Parallelität: Konflikttexte müssen Slotkonflikte und retrybare Serialisierungsfehler gemeinsam abdecken, ohne die DB-Regeln aufzuweichen.
- Authentifizierung: Bestehende Recovery-Codes dürfen bei TOTP-Aktivierung weder ersetzt noch erneut im Klartext ausgegeben werden.
- Secrets: Jeder Keyring-Eintrag muss validiert werden; Development-Material darf in Produktion auch unter umbenannter ID nicht akzeptiert werden.
- UI: Fehlerrendering muss eingegebene Ressourcen und verfügbare Bestätigungsaktionen erhalten; Label-/Footeränderungen bleiben semantisch und No-JavaScript-fähig.

## Umsetzungsschritte

1. Planungslauf ohne Vorschläge per Integrationstest reproduzieren, Run-Metadaten getrennt laden und leere Vorschlagsliste erhalten.
2. Exclusion-Erklärungen gegen Fahrer-Availability und Reservierungen für den gesamten Run-Zeitraum testen und korrigieren.
3. Termin-Swap-Normalisierung, Transport-Preflight, Konflikttext und Assignment-Fehlerzustand mit Unit-/HTTP-Tests absichern und minimal korrigieren.
4. Voice-Retranscription mit fehlender/ungültiger Version als HTTP 422 reproduzieren und die bestehende Fehlerabbildung erhalten.
5. TOTP-Aktivierung mit bestehenden Recovery-Codes sowie vollständige Auth-Keyring-Validierung einschließlich Production-Devmaterial testen und härten.
6. Repositoryvertrag, mobile Auditwerte, Passkey-Label und MFA-Rechts-/Cookie-Hinweise korrigieren und über vorhandene Tests absichern.
7. Generierung, Formatierung, fokussierte Tests, `make check`, risikobasierte E2E-/Race-Prüfungen und Diff-Selbstreview ausführen.
8. Sauberen Commit erstellen, Backup und health-gated Redeploy auf `orderotto-dev` durchführen und öffentlich verifizieren.

## Datenbankänderungen

Keine Migration. Es wird ausschließlich eine sqlc-Lesequery für vorhandene `planning_runs` ergänzt; Schema, Constraints und Daten bleiben unverändert.

## Testplan

- Unit: Appointment-Normalisierung/Transport, Planning-Exclusions, TOTP-Recovery, Konfigurations-Keyring.
- HTTP: Assignment-Re-Render, Konflikttext, Voice-Version 422, Template-Markup.
- Integration: gespeicherter Planungslauf mit leerer Suggestion-Liste.
- Repository/E2E: Production-Compose-Secretvertrag und eindeutige mobile Auditberechnung.
- Abschluss: `make generate`, `make format`, fokussierte Pakete, `make check`; E2E/Race risikobasiert gemäß vorhandener Umgebung.

## Fortschritt

- [x] Reviewtext gelesen und alle 13 Funde am aktuellen Code verifiziert.
- [x] Planungspersistenz und Ausschlusserklärung korrigiert.
- [x] Appointment-/Voice-Flows korrigiert.
- [x] Auth-/Config-Härtung korrigiert.
- [x] UI-/Repositoryverträge korrigiert.
- [x] Vollständige Prüfungen und Selbstreview abgeschlossen.
- [x] Dev-Deployment verifiziert.

## Entdeckungen und Entscheidungen während der Umsetzung

- 2026-08-31: Der Arbeitsbaum ist zu Beginn sauber auf Commit `3e8ba632`.
- 2026-08-31: Für leere Planungsläufe wird eine eigene Run-Query bevorzugt; ein Left Join würde nullable Suggestion-Felder in den generierten Typen und im Mapping unnötig verbreiten.
- 2026-08-31: Auf dem Windows-Host ist `make` nicht installiert. Die darunterliegenden Projektprüfungen wurden deshalb direkt und mit isolierten Go-/Lint-Caches ausgeführt.
- 2026-08-31: Der vollständige E2E-Lauf trifft zwei bestehende Fehler in Task 04 (exakter Focus-Ring-Computed-Style) und Task 08 (Timeout bei der Stale-Proposal-Journey). Beide reproduzieren unverändert im isolierten Worktree auf dem Ausgangscommit `3e8ba632`; der direkt geänderte Task-13-Browseraudit ist grün.
- 2026-08-31: Ein Race-Lauf kann auf diesem Host nicht gebaut werden, weil `CGO_ENABLED=1` einen nicht installierten C-Compiler benötigt. Die normalen Unit- und Integrationstests sind vollständig grün.

## Abschlussnachweis

- Generierung: `scripts/generate-check.sh` grün; templ-Formatierung grün.
- Statische Prüfungen: actionlint, `go vet ./...` und golangci-lint grün (0 Findings).
- Tests: `go test ./... -count=1` grün; Coverage 80,3 % bei 80,0 % Mindestwert; vollständige PostgreSQL-Integrationstests grün.
- Browser: `TestTask13AllMainPagesDesktopAndMobileUsability` grün. Die zwei Fehler des vollständigen E2E-Laufs sind auf dem unveränderten Ausgangscommit reproduziert und damit als vorbestehend eingegrenzt.
- Build: Binary mit Versions-, Commit- und Build-Time-ldflags erfolgreich gebaut.
- Dev-Deployment: Ziel `orderotto-dev`, Verzeichnis `/container/hackwerk`, Compose `/container/hackwerk/compose.yaml`; Image `hackwerk-dev:0.1.60-997984d6-20260831t101345z`, ID `sha256:e9a43c3a6f3f4211aee524a5db8688394cdba0f3521a0df0cd8b7a00623c2ba8`.
- Backup: `hackwerk-20260831T101233Z.dump`, 184864 Byte, Modus 0600 und SHA-256 erfolgreich geprüft. Da keine Migration enthalten ist, war kein neuer Restore-Test erforderlich; der vorherige Restore-Nachweis vom 2026-08-30 bleibt maßgeblich.
- Migration: expliziter Deploy-Schritt erfolgreich, Schema bereits aktuell. App und Worker wurden nacheinander mit `--no-deps` gerollt und verwenden exakt dieselbe neue Image-ID.
- Health: interne App- und Worker-Healthchecks grün; öffentliche `/health/live`, `/health/ready`, `/health/worker` und `/login` liefern HTTP 200, die Loginseite weist Version 0.1.60 aus. Der generische Host-Loopback-Probe ist in diesem bestehenden Dev-Stack nicht anwendbar, weil bewusst kein Host-Port publiziert ist; der tatsächliche Reverse-Proxy-Pfad ist grün.
- Härtung: App und Worker laufen als `65532:65532`, read-only, mit `no-new-privileges`, `cap_drop=ALL`, begrenztem `tmpfs`, read-only Secret-Mounts und ohne Host-Portbindungen. PostgreSQL blieb healthy und wurde einschließlich Datenmount nicht neu erstellt. Der aggregierte Fehler-/Warn-/Failed-/Dead-/Degraded-Zähler der ersten zehn Minuten blieb bei 0.
- Aufräumen: Exakt die lokalen und entfernten Image-/Updater-Transferartefakte wurden entfernt; Backups, Secrets und Datenvolumes blieben erhalten.

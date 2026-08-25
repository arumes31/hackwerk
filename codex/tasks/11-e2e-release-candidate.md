# Task 11 – End-to-End Release Candidate, Demo-Daten und Abnahme

**Empfohlener Aufruf**

```text
$hackplan-release Erzeuge und validiere den Release Candidate gemäß codex/tasks/11-e2e-release-candidate.md.
```

## Ziel

Alle V1-Funktionen werden zu einem konsistenten Release Candidate zusammengeführt und anhand realistischer Nutzerreisen, Rechte-, Parallelitäts-, Zeitzonen-, Security-, Backup- und Browserchecks abgenommen. Dieser Task ergänzt keine großen neuen Features, sondern schließt Lücken, stabilisiert und dokumentiert den Release.

## Vor der Implementierung lesen

- gesamtes `AGENTS.md`, `PLANS.md`
- alle `docs/` und ADRs
- alle `acceptance/*.feature`
- `docs/15-definition-of-done.md`
- alle Task-Abschlussberichte/ExecPlans
- aktuelle OpenAPI-/Betriebsdokumentation

Erstelle `docs/exec-plans/11-e2e-release-candidate.md` mit vollständiger Traceability Task → Akzeptanzszenario → Test.

## Ausgangslage

Tasks 00–10 sind implementiert. Prüfe diese Annahme anhand des Codes und der Tests. Fehlende Pflichtfunktion gilt als Gap und wird in diesem Task behoben, sofern sie zum dokumentierten V1-Scope gehört.

## Scope

### Vollständige Nutzerreisen

Automatisiere und stabilisiere mindestens:

1. **Telefon-/manuelle Erfassung**
   - Fahrer login;
   - Kunde Franz Huber anlegen;
   - Auftrag 80 m³, 180 Minuten, Wunsch Anfang September;
   - Warteliste;
   - Admin sieht Eintrag.

2. **Planen und Bestätigen**
   - Admin erzeugt Vorschläge oder plant manuell;
   - übernimmt Proposal;
   - weist Fahrer und Hackmaschine zu;
   - fixiert explizit;
   - Worker versendet an Fake-SMTP;
   - Kunde bestätigt über öffentlichen Link;
   - Dashboard/Kalender zeigt bestätigt;
   - Fahrer sieht denselben Termin und Maps-Link.

3. **Ablehnung und Neuplanung**
   - Kunde lehnt ab;
   - Reservierung bleibt;
   - Admin verschiebt/storniert bewusst;
   - alter Token ist ungültig;
   - neue Bestätigung wird versandt.

4. **Konflikt/Race**
   - zwei Adminsessions versuchen denselben exklusiven Slot/Ressource;
   - genau eine Mutation gewinnt, andere bekommt 409/revert;
   - kein inkonsistenter Job-/Outbox-/Wartelistenstatus.

5. **Fahrer-Verfügbarkeit**
   - Fahrer erfasst Woche und Urlaub;
   - Admin sieht Konflikt;
   - Override nur mit Grund, keine physische Doppelbelegung.

6. **Spracheingabe**
   - Fakeaudio/Fixture → Transkript → strukturierter Review;
   - Korrektur und Commit;
   - Auftrag landet Warteliste;
   - kein Termin wird automatisch erzeugt.

7. **ICS**
   - Feed erzeugen;
   - Termin sichtbar;
   - Move erhöht Sequence;
   - Widerruf beendet Zugriff.

8. **Betrieb**
   - Queue-/Providerfehler und Retry;
   - Backup/Restore;
   - deaktivierter Benutzer verliert Sessions und Feed.

### Acceptance Traceability

- Jede Scenario-Zeile in `acceptance/*.feature` erhält automatisierten Test oder explizit begründeten manuellen Nachweis.
- Erzeuge `docs/release/acceptance-matrix.md` mit Feature/Scenario, Testdatei, Status und Evidenz.
- Keine „temporarily skipped“ kritischen Szenarien.
- Flaky Tests beheben, nicht nur Retries erhöhen. Zeit/Uhr/Provider injizieren.

### Daten und Demo

- `seed-dev` erzeugt realistische, datenschutzneutrale Demo:
  - 1 Admin, 5 Fahrer/Benutzer bzw. insgesamt 6 Benutzer;
  - eine Hackmaschine, Transportressource;
  - Huber/Maier/Berger plus weitere Aufträge;
  - Warteliste, Vorschläge, fixierte/offene/bestätigte/abgelehnte/erledigte Termine;
  - Availability und Abwesenheit;
  - Notificationfehler für Adminsicht.
- Seed ist idempotent oder verweigert Doppelanlage verständlich.
- Produktionsbinary seedet niemals automatisch.

### Kompatibilität und UX-Abnahme

- aktuelle Chromium-, Firefox- und WebKit-Playwright-Projekte, soweit CI-Umgebung unterstützt;
- Viewports 360 px, Tablet, Desktop;
- Kernflow nur per Tastatur;
- de-AT Datums-/Zeit-/Umlautdarstellung;
- kein horizontaler Overflow bei Kernseiten;
- verständliche Offline-/Provider-/409-/422-Fehler;
- Browser-Zeitzone explizit `Europe/Vienna` sowie mindestens ein Test mit abweichender Clientzeitzone, Server bleibt korrekt.

### Datenbank und Upgrade

- Migration von leerer DB bis Head;
- Upgrade von einem dokumentierten vorherigen Snapshot/Meilenstein, soweit im Projekt verfügbar;
- Down-/Rollbackgrenzen dokumentiert;
- Schema-Constraints und Indizes via Test/Inspection;
- Backup von Releasekandidat, Restore in frische DB, E2E-Smoke danach.

### Security- und Privacy-Abnahme

- führe Reviewtasks 90–93 in separaten Codex-Threads/Agenten aus, falls Umgebung dies erlaubt; ansonsten strikt getrennte Reviewdurchgänge;
- behebe alle kritischen/hohen Findings;
- teste IDOR/RBAC, CSRF, Sessionfixation, Tokenleak, SSRF, XSS, Upload, Rate Limit, Logredaction, Feedrevocation;
- Dependency-/Image-/Secret-Scans ohne unbewertete kritische/hohe Findings;
- `docs/release/security-review.md` mit Finding, Schwere, Fix, Test.

### Performance-/Kapazitäts-Smoke

Mit synthetischen Daten mindestens:

- 5.000 Kunden;
- 10.000 Aufträge;
- 2.000 aktive/historische Termine;
- 500 Wartelisteneinträge;
- 6 Fahrer und mehrere Ressourcen.

Prüfe paginierte Listen, Kalenderwoche, Dashboard und Vorschlagsberechnung. Definiere realistische lokale Zielwerte und dokumentiere Hardware/Umgebung; keine unbelegten Produktions-SLAs versprechen. Nutze `EXPLAIN (ANALYZE, BUFFERS)` für auffällige Queries und behebe fehlende Indizes/N+1.

### Releaseartefakte und Dokumentation

- SemVer-Version, Changelog, Buildmetadata;
- reproduzierbares Image mit Tag/optional Digest;
- SBOM und Scanreport;
- Admin-/Benutzerhandbuch mit Screens/Schrittfolgen soweit praktikabel;
- Produktions-Runbook, Konfigurationsreferenz, Backup/Restore, Upgrade;
- Known Limitations: read-only ICS, providerneutrales SMS-Webhook, keine autonome Planung, keine Audioaufbewahrung, kein bidirektionaler Sync;
- `docs/release/GO-LIVE-CHECKLIST.md` mit Verantwortlichem/Statusfeldern.

### Release Gate

`make release-check` muss mindestens ausführen oder orchestrieren:

- generate check;
- format/lint/vet;
- unit/integration/race;
- E2E kritische Matrix;
- migrations fresh/upgrade;
- backup/restore smoke;
- security/dependency/image/secret scans;
- container non-root/read-only smoke;
- documentation/link validation.

## Verbindliche Regeln

- Keine neue große Funktion, nur Scope-Lücken und Stabilisierung.
- Keine übersprungenen kritischen Akzeptanztests.
- Keine Testdaten/Secrets im Produktionsimage.
- Keine Freigabe mit unbewerteten kritischen/hohen Securityfindings.
- Releasebericht nennt Unsicherheiten/Limitierungen transparent.
- Ein grüner Browserflow ersetzt keine DB-/Concurrencytests.

## Akzeptanzkriterien

- [ ] Alle V1-User-Journeys funktionieren Ende-zu-Ende.
- [ ] Acceptance Matrix ist vollständig und ohne kritische Lücken.
- [ ] Rechte-/Concurrency-/DST-/Outbox-/Tokenregeln sind automatisiert nachgewiesen.
- [ ] Chrome/Firefox/WebKit und 360px/Desktop-Kernflows bestehen oder begründete Plattformgrenze ist dokumentiert.
- [ ] Backup/Restore und Migrationen sind mit Releasebuild geprüft.
- [ ] Securityreviews 90–93 sind abgeschlossen; kritische/hohe Findings behoben.
- [ ] Performance-Smoke zeigt keine offensichtlichen N+1/unbegrenzten Queries.
- [ ] Image ist reproduzierbar, non-root und gescannt; SBOM vorhanden.
- [ ] Handbuch, Runbook, Changelog, Known Limitations und Go-Live-Checkliste liegen vor.
- [ ] `make release-check` läuft erfolgreich.

## Pflichtprüfungen

```bash
make clean
make generate-check
make format-check
make lint
make test
make test-integration
make test-race
make test-e2e
make scan
make build
make check
make release-check
```

Danach Produktionsimage starten, Seed in isolierter Demo-Umgebung laden, vollständigen Smoke ausführen, Backup/Restore durchführen und Build-Digest dokumentieren.

## Abschlussbericht

Erzeuge `docs/release/RELEASE-REPORT.md` mit Version/Digest, Featurestatus, Acceptance-/Test-/Scanresultaten, Migration/Restore, Performance, behobenen Reviewfindings, Known Limitations und Go-/No-Go-Empfehlung. Eine Go-Empfehlung nur bei erfüllten Gates.

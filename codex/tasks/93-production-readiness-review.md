# Review 93 – Production Readiness, Deployment, Recovery und Release-Gate

**Empfohlener Aufruf**

```text
$hackplan-review Prüfe Produktionsbereitschaft gemäß codex/tasks/93-production-readiness-review.md. Behebe bestätigte Blocker/High Findings.
```

## Ziel

Bewerte, ob ein Betreiber HackWerk nachvollziehbar installieren, konfigurieren, aktualisieren, überwachen, sichern, wiederherstellen und bei Störungen betreiben kann. Ein grüner Featuretest allein genügt nicht.

## Vor dem Review lesen

- `AGENTS.md`
- `docs/02-architecture.md`
- `docs/10-security-privacy.md`
- `docs/11-test-strategy.md`
- `docs/12-operations-deployment.md`
- `docs/14-configuration.md`
- `docs/15-definition-of-done.md`
- alle ADRs, Docker-/Compose-/CI-Dateien, Makefile, Runbooks und Releaseartefakte

## Reviewmethode

- Starte von einer sauberen Checkout-/Buildumgebung.
- Verwende nur dokumentierte Befehle und Beispielconfig.
- Teste Happy Path und Störungen praktisch.
- Erzeuge `docs/reviews/93-production-readiness-review.md`.
- Findings mit Schwere, Auswirkung auf Verfügbarkeit/Daten/Recovery, Reproduktion und Fix.

## Build und Supply Chain

- Versionen/Lockfiles gepinnt, reproduzierbare Generatoren;
- keine `latest`-Tags in Releasepfad;
- CI und lokal nutzen dieselben Befehle;
- Multi-stage Build ohne Secrets/Source-Leaks im Runtimeimage;
- non-root/read-only, Capabilities/Volumes/Temp korrekt;
- SBOM, Checksums/Digest, Vulnerability-/Secret-/Dependency-Scans;
- Buildmetadata/Changelog/SemVer;
- Imagegröße und Plattformen dokumentiert.

## Konfiguration und Secrets

- vollständige Configreferenz, Typen, Defaults, Required, Secrets;
- Produktion verweigert Devdefaults;
- `*_FILE`/Secretmount funktioniert;
- Schlüsselrotation und Widerruf von Feed-/Confirmation-/Sessionzugängen;
- Trusted Proxy, Host, Public Base URL, TLS, externes SMTP, SMS, Routing, Speech;
- Featureflags mit sauberem Verhalten;
- keine Konfigurationswerte/Secrets in Logs/Health.

## Deployment und Upgrade

- frische Installation ausschließlich nach Doku;
- Migrationlock, Reihenfolge, Exitcodes und Wiederholbarkeit;
- App/Worker-Versionkompatibilität während Rollout;
- Backward-/Forward-Kompatibilität der Outboxpayloads;
- Rolling oder Stop-the-world-Strategie klar dokumentiert;
- Rollbackgrenzen bei nicht rückwärtskompatibler Migration;
- Smokecheck nach Deployment;
- Wartungsmodus/Fehlerseite bei Migration oder DB-Ausfall;
- keine automatische Dev-Seeds in Produktion.

## Health, Monitoring und Alerting

- Liveness/Readiness korrekt und schnell;
- Workerheartbeat/Outboxalter;
- DB-Pool und Queryfehler;
- Notificationfailures/Dead Letter;
- Voice-/Routingproviderdegradation;
- Disk/DB/Backupalter als Betreiberaufgabe dokumentiert;
- Metriken ohne PII/High Cardinality;
- vorgeschlagene Alerts mit Schwelle/Runbook, aber keine unbelegten Universalwerte;
- Logs strukturiert, Request-ID korrelierbar, Rotation extern dokumentiert.

## Backup, Restore und Disaster Recovery

Praktisch testen:

1. realistischen Seed erzeugen;
2. Backup während laufender App;
3. DB/Volume isoliert als verloren simulieren;
4. in neue DB restoren;
5. Migrationstatus prüfen;
6. App/Worker starten;
7. Kern-E2E und Counts/Checksums prüfen;
8. Feed-/Session-/Confirmationtokenverhalten nach Restore bewerten.

Prüfe:

- Retention, Offsite, Verschlüsselung, Rechte;
- Backupüberwachung/Fehlerexitcodes;
- RPO/RTO als Betreiberentscheidung;
- Restore-Doku vollständig;
- temporäres Voiceaudio und Secrets korrekt ausgeschlossen/separat;
- Point-in-time Recovery als optionales Backlog, falls nicht implementiert.

## Failure Injection

Simuliere mindestens:

- PostgreSQL kurz weg / Neustart;
- SMTP/SMS/Routing/Speech timeout/down;
- Worker gestoppt und später neu gestartet;
- Outboxevent hängt/Lease läuft aus;
- DB-Verbindungspool erschöpft;
- ungültige/fehlende Secretdatei;
- read-only filesystem/volles Tempvolume;
- Migration fehlschlägt;
- Reverse Proxy sendet untrusted Forwarded Header;
- Backupziel nicht beschreibbar.

Erwartung: kontrollierte Fehler, keine Datenkorruption, klare Metrics/Logs/Runbook, Recovery ohne manuelles DB-Basteln.

## Kapazität und Performance

- synthetischer Datensatz gemäß Task 11;
- Startzeit/Memory/CPU als Beobachtung dokumentieren;
- Dashboard/Kalender/Warteliste/Vorschläge mit begrenzten Queries;
- DB-Indizes/Explain;
- Outboxdurchsatz/Backlogabbau;
- Upload-/Workerparallelität begrenzt;
- keine unlimitierten Response-/Historienbereiche;
- Produktions-Sizing als Startpunkt, keine Garantie.

## Operational Security

- DB/Metrics/Debug nicht öffentlich;
- Admin-Credentials/erstes Passwort sicher provisioniert;
- TLS-/Proxyvertrauen;
- Audit-/Logzugriff;
- Tokenleck-Runbook und Feedrotation;
- verlorenes Fahrergerät: Sessions widerrufen/Benutzer deaktivieren;
- Providerkeyrotation;
- Scan-/Patchprozess und Updatefrequenz.

## Dokumentationstest

Lass einen frischen Ablauf ausschließlich mit Dokumentation durchführen:

- Installation;
- initialer Admin;
- sechs Demo-/Produktivbenutzer anlegen;
- Provider konfigurieren;
- Backup/Restore;
- Update;
- Diagnose einer fehlgeschlagenen Nachricht;
- Feed widerrufen;
- Voice deaktivieren.

Jede implizite Annahme ist ein Doku-Finding.

## Abschlusskriterien

- [ ] Clean build/install aus Doku erfolgreich.
- [ ] Migration/Upgrade/Rollbackgrenze nachvollziehbar.
- [ ] Failure-Injection ohne Korruption und mit sichtbarer Diagnose.
- [ ] Backup/Restore praktisch nachgewiesen.
- [ ] Monitoring/Alert/Runbook deckt Kernstörungen ab.
- [ ] Produktionsconfig und Secrets sicher.
- [ ] Image/Scans/SBOM/Releaseartefakte vollständig.
- [ ] Blocker/High behoben oder klare NO-GO-Empfehlung.
- [ ] Betriebslimitierungen transparent dokumentiert.

## Abschlussbericht

Schreibe `docs/reviews/93-production-readiness-review.md` mit Environment, exakten Befehlen, Findings/Fixes, Recoverynachweis, Failure-Injection, Performancebeobachtung und `GO`/`NO-GO`/`GO WITH CONDITIONS`.

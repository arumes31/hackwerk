# Task 10 – Production Hardening, Observability, Backup/Restore und Betriebswerkzeuge

**Empfohlener Aufruf**

```text
$hackplan-implement Implementiere codex/tasks/10-hardening-observability-backup.md vollständig.
```

## Ziel

HackWerk erhält eine belastbare, dokumentierte Produktionsbasis: sichere Container, Header/Rate Limits, redigierte Logs, Metriken, Healthchecks, Migration-/Workerbetrieb, Backup- und Restore-Verfahren, Dependency-/Image-Scans sowie eine Referenzkonfiguration hinter TLS-Reverse-Proxy.

## Vor der Implementierung lesen

- `AGENTS.md`, `PLANS.md`
- `docs/02-architecture.md`
- `docs/10-security-privacy.md`
- `docs/11-test-strategy.md`
- `docs/12-operations-deployment.md`
- `docs/14-configuration.md`
- `docs/15-definition-of-done.md`
- `codex/tasks/90-security-review.md`
- aktuelle Implementierung aller Provider/öffentlichen Endpunkte

Erstelle `docs/exec-plans/10-hardening-observability-backup.md`.

## Scope

### HTTP- und Anwendungshärtung

- Vollständige Security-Header-Middleware: CSP mit Nonce/Hash für notwendige Scripts, `frame-ancestors`, `nosniff`, Referrer Policy, Permissions Policy, HSTS nur bei korrekt erkanntem HTTPS und dokumentiertem Proxyvertrauen.
- Trusted-Proxy-Konfiguration als explizite CIDR-Liste; Forwarded Headers nur von vertrauenswürdigen Proxies akzeptieren.
- Host Allowlist, Request Body Limits, Headerlimits, Read/Write/Idle Timeouts.
- differenzierte Rate Limits für Login, öffentliche Confirmation, Kalenderfeed, Voice Upload, Providercallbacks und interne Seiten; zuverlässiger Storage ohne neue persistente Infrastruktur, dokumentierte Single-/Multi-Replica-Semantik.
- CSRF-/Origin-Audit aller mutierenden Endpunkte, Content-Type Enforcement und JSON-Decoder mit unbekannten Feldern abweisen, wo sinnvoll.
- einheitliche sichere Fehler-IDs; interner Fehler mit Request-ID, keine SQL-/Providerdetails im Browser.
- Schutz vor CSV/ICS/Formel-Injection bei späteren Exporten, auch wenn CSV nur vorbereitet ist.

### Secrets und Konfiguration

- Startvalidierung für alle kritischen Variablen, sichere Secret-File-Unterstützung (`*_FILE`) für Container Secrets.
- Schlüsselrotationstrategie für Session-/Payload-/Token-nahe Schlüssel dokumentieren; keine Roh-Tokens ableitbar.
- Konfigurationsdump/Diagnose zeigt nur Namen und redigierte Werte.
- Produktionsmodus verweigert unsichere Defaults, deaktivierte TLS-Prüfung, bekannte Dev-Passwörter, Debugtemplates und wildcard Hosts.
- Featureflags für SMS, E-Mail, Voice, Routing, ICS mit klarer UI/Health-Semantik.

### Logging, Audit und Metriken

- strukturiertes `slog`-Schema: timestamp, level, service, version, request_id, event, duration, status; PII-Redaction zentral.
- Audit-Trail ist fachlich, append-only und getrennt von technischen Logs; Admin-Viewer mit Filter/Export nur falls datenschutzkonform und im Scope klein genug.
- Prometheus-kompatibler `/metrics`-Endpunkt auf separater interner Bindadresse oder mit starker Zugriffsbeschränkung.
- Metriken mindestens:
  - HTTP Requests/Latenz/Fehler;
  - DB-Pool;
  - Outbox queue age/status/retries;
  - Notifications sent/failed nach Kanal ohne Empfänger;
  - Planungsdauer/Kandidaten;
  - Voicejobs/status ohne Inhalt;
  - Health/build info.
- Keine Kundennamen, Telefonnummern, E-Mails, Pfadtokens oder beliebige hohe Kardinalität in Labels.
- optional OpenTelemetry-Port nur vorbereiten, keine unnötige Collectorabhängigkeit.

### Health und Betriebsmodi

- Liveness prüft Prozesszustand, nicht externe Provider.
- Readiness prüft DB, Migrationkompatibilität und notwendige interne Voraussetzungen; Providerdegradation wird getrennt angezeigt, damit SMS-Ausfall nicht zwingend Webzugriff abschaltet.
- Worker-Health/Heartbeat und Queue-Alter sichtbar.
- Migrationslock verhindert parallele inkompatible Migrationen.
- Deploymentreihenfolge dokumentiert: Backup, Migrate, App/Worker Rollout, Smoke, Rollbackgrenzen.

### Docker/Compose Production Reference

- finale Images non-root, read-only root filesystem, notwendige tmpfs/Volumes, `no-new-privileges`, begrenzte Capabilities, sinnvolle ulimits/PIDs.
- SBOM/Provenance soweit mit gewählter Toolchain praktikabel.
- `compose.prod.example.yaml` hinter beispielhaftem Reverse Proxy oder klare Proxyvertragsdokumentation; keine echten Zertifikate/Secrets.
- PostgreSQL nicht öffentlich exponieren; separates Netzwerk; Volumes/Backupmounts.
- Ressourcenlimits als dokumentierte Ausgangswerte, nicht als universelle Garantie.

### Backup und Restore

Implementiere dokumentierte Scripts/Commands:

- konsistentes PostgreSQL-Backup (`pg_dump` custom format oder begründete Alternative), Zeitstempel, Kompression, restriktive Rechte;
- optional Verschlüsselung via extern verwaltetem Schlüssel/Tool, keine hardcodierten Keys;
- Retention/Rotation mit Dry-run und Schutz vor Pfadfehlern;
- Restore in neue leere DB mit Versions-/Migrationsprüfung;
- automatisierter Restore-Test in CI oder regelmäßigem Job gegen Fixture-Daten;
- Runbook für RPO/RTO-Entscheidung, Offsitekopie, Monitoring und regelmäßigen Test;
- Voice-tempfiles sind nicht Backupbestandteil; Secrets werden separat gesichert.

### Dependency-/Security Scans

- `govulncheck` gepinnt/integriert;
- OSV/Dependency Audit für Go und Node-Lockfile;
- Containerimage-Scan mit gepinntem Tool/CI-Action;
- Secret Scan und SBOM;
- klare Policy: kritische/hohe produktiv ausnutzbare Findings blockieren Release, Ausnahmen mit Datum/Owner/Begründung.
- `make scan` lokal sinnvoll ausführbar oder dokumentiert in Teilcommands.

### Datenschutz/Betriebsdokumentation

- Datenkategorien, Retention und Lösch-/Archivierungsstrategie;
- Export/Anfrage betroffener Kunden als späterer Prozess oder minimaler Adminflow, ohne Historie unkontrolliert zu löschen;
- Providerdatenflüsse (externes SMTP, SMS, Routing, Speech) und notwendige Verträge/Regionen als Deploymententscheidung;
- Incident Runbooks: Provider down, Queue wächst, DB voll, Tokenleck, verlorenes Gerät/Feedlink, Restore.
- Wartungsmodus mit verständlicher Seite und Admin-/Healthzugriff, falls sinnvoll.

### Tests

- Header-/CSP-/Cookie-/Proxytests;
- Logcapture mit PII-/Token-Canaries und Assertion, dass nichts durchsickert;
- Rate-Limit- und Body-Limit-Tests;
- Backup/Restore mit Seed, Prüfsummen/Counts und anschließenden Integrationstests;
- Container-Smoke unter read-only root FS/non-root;
- Readiness bei DB/Migrations-/Worker-/Providerzuständen;
- Scanbefehle in CI.

## Verbindliche Regeln

- Keine Security durch Reverse Proxy allein; Anwendung validiert eigene Vertrauensgrenzen.
- Keine PII/Secrets in Metricslabels, Logs, Traces, Crashreports oder Healthresponses.
- Readiness darf Providerausfall differenziert behandeln.
- Backup gilt erst nach Restore-Test als vorhanden.
- Produktionsconfig startet nicht mit unsicheren Devdefaults.
- Keine neue Infrastrukturkomponente ohne ADR und klaren Nutzen.

## Nicht Bestandteil

- Kubernetes/Helm als Pflicht;
- SIEM-/APM-Herstellerbindung;
- Hochverfügbarkeits-PostgreSQL-Automatisierung;
- vollständige rechtliche Datenschutzberatung.

## Akzeptanzkriterien

- [ ] Produktionsimage läuft non-root mit read-only root FS und nur notwendigen Schreibpfaden.
- [ ] Security Header, CSRF, Host/Proxytrust, Limits und Rate Limits sind getestet.
- [ ] Logs/Metriken enthalten keine PII/Tokens; Canarytests belegen dies.
- [ ] Readiness/Liveness/Workerhealth sind fachlich korrekt getrennt.
- [ ] Outbox-/Providerprobleme sind über Metriken/Runbook erkennbar.
- [ ] Backup und automatisierter Restore-Test funktionieren.
- [ ] Produktionsmodus verweigert unsichere Konfiguration.
- [ ] `make scan` prüft Go, Node, Secrets und Image/SBOM angemessen.
- [ ] Deployment-, Rollback-, Restore- und Incident-Runbooks sind vollständig genug für Betrieb.
- [ ] Kritische/hohe Findings sind behoben oder formal dokumentiert außerhalb Release.

## Pflichtprüfungen

```bash
make generate
make format
make lint
make test
make test-integration
make test-e2e
make test-race
make build
make scan
make check
```

Zusätzlich: Container unter finalen Securityoptionen starten, Backup erzeugen, in frische DB restoren und Smoke-/Integrationstest ausführen.

## Abschlussbericht

Liefere Threat-/Hardening-Zusammenfassung, aktive Header/Limits, Log-/Metricschema, Backup-Restore-Nachweis, Scanresultate, Produktionsstartanleitung und bewusst verbleibende Deploymententscheidungen.

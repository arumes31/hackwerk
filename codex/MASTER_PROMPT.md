# HackWerk – One-Shot Master Prompt für Codex

> Empfohlen ist die schrittweise Abarbeitung der einzelnen Dateien unter `codex/tasks/`. Dieser Prompt ist für einen langen autonomen Build in einem neuen oder nahezu leeren Repository gedacht.

## Auftrag

Baue **HackWerk** vollständig als produktionsnahen, responsiven Docker-/Go-Webservice gemäß diesem Repository.

Arbeite nicht nur konzeptionell. Erzeuge die Anwendung, Migrationen, Tests, Assets, Dokumentation, Docker-Images und Betriebswerkzeuge. Verwende den vorhandenen Prompt-Paket-Inhalt als verbindliche Produktspezifikation.

## Startreihenfolge

1. Lies `AGENTS.md` vollständig.
2. Lies `README.md`, `PLANS.md` und alle Dateien unter `docs/`, `docs/adr/`, `reference/` und `acceptance/`.
3. Prüfe den aktuellen Repository-Zustand und das Git-Remote.
4. Erstelle `docs/exec-plans/MASTER-build.md` nach `PLANS.md` und halte es während der Umsetzung aktuell.
5. Implementiere die Tasks `codex/tasks/00-repository-bootstrap.md` bis `codex/tasks/11-e2e-release-candidate.md` **in numerischer Reihenfolge**.
6. Führe danach die Reviews `90` bis `93` aus und behebe alle Findings mit Schweregrad kritisch/hoch sowie alle leicht behebbaren mittleren Findings.
7. Führe die vollständige Validierung aus und liefere einen präzisen Abschlussbericht.

## Verbindliche Kernregeln

- Nur Admins planen, verschieben, vergrößern/verkleinern, fixieren, absagen oder öffnen Termine.
- Fahrer sehen alle geplanten Termine und dürfen Kunden/Aufträge erfassen, Warteliste ergänzen, eigene Verfügbarkeit pflegen und Bemerkungen anlegen.
- Drag-and-drop ist nie gleichbedeutend mit Fixierung. Es erzeugt einen Entwurf/Vorschlag.
- Fixierung ist eine explizite Admin-Aktion und schreibt Termin, Reservierungen, Kundenbestätigungsanforderung und Outbox-Event atomar.
- Spracheingabe und Planungsvorschläge erzeugen nur prüfbare Entwürfe; keine automatische Speicherung/Fixierung.
- Kunde, Auftrag, Termin, Fahrer und Ressource sind getrennte Aggregate.
- Keine Geschäftslogik darf genau eine Maschine hardcoden.
- PostgreSQL ist die einzige persistente System-of-Record-Datenbank in V1; kein Redis.
- Zeiten in UTC/`timestamptz`, Anzeige und Eingabe in `Europe/Vienna`; Sommer-/Winterzeit testen.
- Benachrichtigungen ausschließlich über Transactional Outbox und Worker.
- Kundenlinks und Kalenderfeed-Tokens sind kryptographisch sicher, gehasht gespeichert, widerrufbar und in Logs redigiert.
- Kein CDN zur Laufzeit. Browserassets werden lokal gebaut und ausgeliefert.
- UI ist deutsch (`de-AT`), responsive, tastaturbedienbar und kommuniziert Status nicht nur über Farben.

## Technische Zielarchitektur

- Go `1.27.x`, `chi/v5`, `pgx/v5`, `sqlc`, `templ`;
- PostgreSQL `18.x` mit Exclusion Constraints für exklusive Fahrer-/Ressourcenreservierungen;
- `htmx 2.x` für einfache Interaktionen;
- TypeScript + esbuild nur für FullCalendar, Drag-and-drop, Audioaufnahme und komplexere UI-Interaktion;
- FullCalendar Standard-Plugins, keine Premium-Abhängigkeit;
- ein Binary mit `serve`, `worker`, `migrate`, `seed-dev`, `admin`, `healthcheck`;
- Multi-stage Docker Build, non-root, Healthchecks, Compose für Entwicklung und produktionsnahe Referenz;
- externer SMTP-Versandadapter, generischer signierter SMS-Webhook-Adapter, Fake-/Log-Adapter; kein lokaler Maildienst und kein E-Mail-Empfang;
- optionaler OpenAI-Speech-to-Text-Adapter hinter Interface; kein Providerzwang;
- regelbasierte, erklärbare Planung mit Haversine-Fallback und optionalem Routingprovider;
- ICS-Export und private, widerrufbare read-only Kalenderfeeds.

## Arbeitsweise

- Behandle jede Task als vertikalen, vollständig getesteten Meilenstein.
- Aktualisiere den ExecPlan nach jedem größeren Schritt.
- Nutze Migrationen für jede Schemaänderung; editiere generierten Code nicht manuell.
- Halte Handler dünn; Geschäftslogik gehört in Application Services und Domain.
- Nutze injizierbare Uhr, ID-/Token-Generatoren und Providerports.
- Autorisierung wird in Services/Use Cases erneut geprüft, nicht nur in UI/Handlern.
- Implementiere serverseitige Konfliktprüfung und DB-Constraints. Browserprüfung ist nur Komfort.
- Nutze optimistic concurrency über `version`; bei Konflikt HTTP 409 und UI-Revert/Reload.
- Bewahre Historie; archiviere statt unkontrolliert zu löschen.
- Führe neue Abhängigkeiten nur begründet ein und pinne Versionen reproduzierbar.
- Keine offenen TODOs innerhalb des vereinbarten Scopes.

## Qualitätsgates

Nach jedem Task mindestens:

```bash
make generate
make format
make lint
make test
make test-integration
make build
```

Vor Abschluss zusätzlich:

```bash
make test-e2e
make test-race
make scan
make check
make release-check
```

Falls ein Befehl in der aktuellen Umgebung technisch nicht ausgeführt werden kann, implementiere ihn dennoch korrekt, führe alle möglichen Teilprüfungen aus und dokumentiere exakt Ursache und verbleibenden Nachweis. Keine Tests kommentarlos überspringen.

## Abschlussbericht

Liefere am Ende:

1. implementierte Meilensteine und sichtbare Benutzerflüsse;
2. Architektur- und Schemaübersicht;
3. ausgeführte Befehle mit Ergebnis;
4. Sicherheits-/Concurrency-Selbstreview und behobene Findings;
5. bekannte, klar außerhalb V1 liegende Restpunkte;
6. lokale Startanleitung inklusive initialem Admin und Beispieldaten;
7. Produktions-Checkliste für Secrets, TLS/Reverse Proxy, externes SMTP, SMS, Backup und Monitoring.

Beginne jetzt mit dem Repository-Audit und dem ExecPlan. Implementiere anschließend ohne auf eine erneute Freigabe zu warten.

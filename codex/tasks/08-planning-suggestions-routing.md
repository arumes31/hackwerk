# Task 08 – Erklärbare Termin- und Routenplanungsvorschläge

**Empfohlener Aufruf**

```text
$hackplan-implement Implementiere codex/tasks/08-planning-suggestions-routing.md vollständig.
```

## Ziel

Für einen Wartelistenauftrag kann der Administrator drei nachvollziehbare, konfliktfreie Terminvorschläge erzeugen. Die Engine berücksichtigt Wunschzeitraum, Dringlichkeit, Hack-/Transportdauer, Fahrer- und Ressourcenverfügbarkeit, vorhandene Termine, freie Zeitblöcke und geografische Nähe. Sie fixiert niemals selbstständig.

## Vor der Implementierung lesen

- `AGENTS.md`, `PLANS.md`
- `docs/03-domain-model.md`
- `docs/04-status-state-machine.md`
- `docs/07-api-and-integrations.md`
- `docs/08-planning-engine.md`
- `docs/10-security-privacy.md`
- `docs/14-configuration.md`
- `acceptance/planning.feature`
- vorhandene Kalender-/Availability-/Ressourcenservices

Erstelle `docs/exec-plans/08-planning-suggestions-routing.md`. Dokumentiere Algorithmus und deterministische Tie-Breaker.

## Scope

### Planungsports und Datenmodell

Definiere kleine Interfaces für:

- Kalender-/Reservierungsabfrage;
- Availability;
- Ressourcenanforderung;
- Entfernungs-/Fahrzeitmatrix;
- Clock und Konfiguration;
- optional Persistenz von `planning_runs` und `planning_suggestions` mit Inputversion, Scorebestandteilen, Erzeugungs-/Ablaufzeit und Annahmestatus.

Vorschläge dürfen verworfen/reproduziert werden. Sie sind keine Reservierung. Beim Übernehmen wird serverseitig vollständig neu validiert.

### Harte Ausschlusskriterien

Ein Kandidat ist ungültig bei:

- Zeit außerhalb konfigurierter Planungs-/Betriebsfenster;
- Überschneidung exklusiver Fahrer/Ressource;
- kein aktiver/verfügbarer Fahrer, sofern kein in der UI später begründbarer Override;
- keine Hackressource;
- Transportauftrag ohne erfüllbare interne Ressource oder explizit bestätigten externen Transport;
- Zeitraum/Dauer reicht nicht;
- Auftrag/Version hat sich verändert oder bereits aktiver Termin;
- Slot liegt in Vergangenheit oder außerhalb maximalem Planungshorizont.

Diese Regeln sind Gates, keine bloßen negativen Scores.

### Kandidatengenerierung

- Erzeuge Slots in konfigurierbarer Granularität, z. B. 15 Minuten.
- Nutze Wunschzeitraum, Freitext nicht als unkontrollierte Logik; strukturierter Zeitraum dominiert.
- Bei „möglichst bald“/hoher Dringlichkeit beginne früh, bleibe innerhalb Horizont.
- Berücksichtige vollständige blockierte Dauer: Hackdauer + Transportdauer + planbarer Buffer gemäß dokumentierter Policy.
- Kandidaten vor/nach vorhandenen Terminen desselben Tages und in freien Tagesblöcken.
- Begrenze Kandidatenzahl und DB-/Routingaufrufe; keine kombinatorische Explosion.
- Stabile Sortierung/Tie-Breaker, sodass identischer Zustand identische Top-3 liefert.

### Scoremodell

Berechne 0–100 bzw. klar normalisierten Score mit nachvollziehbaren Komponenten:

- Erfüllung Wunschzeitraum;
- Dringlichkeit/Alter der Warteliste;
- Fahrer-Verfügbarkeit/Qualität (verfügbar besser als limited/Override);
- Ressourcen-/Transportpassung;
- Fahrstrecke/Fahrzeit vom vorherigen und zum nächsten Termin;
- geografische Gruppierung/Region;
- Nutzung von Lücken ohne unpraktische Reststücke;
- zeitlicher Puffer/Risiko;
- optional frühe Planung.

Gewichte aus validierter Admin-Konfiguration mit sicheren Defaults. Zeige keine falsche mathematische Präzision: Score und kurze Gründe, z. B. „Fahrer verfügbar“, „12 km vom vorherigen Auftrag“, „im Wunschzeitraum“, „füllt 3-Stunden-Lücke“.

### Routing/Distanz

Implementiere Providerport:

```text
Matrix(points) -> distances, durations, source, freshness
```

Adapter:

1. `Haversine` als immer verfügbarer Offline-Fallback, klar als Luftlinie markiert;
2. generischer HTTP-Routingadapter (z. B. OSRM-kompatibel) mit ausschließlich konfigurierter Base-URL, Timeouts, Größenlimits, Caching und Circuit-Breaker/Backoff in angemessenem Umfang;
3. Fake für Tests.

- Kein Karten-/Routingproviderzwang.
- Fehlende Koordinaten reduzieren Routen-Score und erzeugen Hinweis, blockieren aber nicht zwingend einen sonst validen Vorschlag.
- Cache nur technische Distanzwerte/Koordinatenpaare mit TTL, vorzugsweise PostgreSQL oder bounded in-process; keine neue persistente Datenbank.
- Batch/Matrix statt N×Einzelrequests.
- Externe Provider erhalten nur notwendige Koordinaten, keine Namen/Telefonnummern.

### Geografische Gruppenhinweise

- Analyse über Wartelisteneinträge in einem begrenzten Zeitraum/Region.
- Erkenne Cluster mit konfigurierbarer Entfernung und Mindestanzahl.
- UI-Hinweis: „Diese drei Aufträge liegen geografisch nahe. Gemeinsame Planung kann Wege reduzieren.“
- Der Hinweis ist erklärbar und führt zu gefilterter Warteliste/Planungsansicht, fixiert nichts.

### UI

- Bereich „Planungsvorschläge“ und Aktion im Wartelistendetail.
- Zeige Top 3 mit Datum, Start/Ende, Dauer, Fahrer, Ressourcen, Transport, Score, positiven/negativen Gründen und Distanzquelle.
- Stern/„optimal“ nur für höchsten validen Kandidaten, nicht als Garantie.
- Aktionen:
  - „Vorschlag übernehmen“ erzeugt nach erneuter Validierung einen `proposal`/Draft im Kalender;
  - „Im Kalender ansehen“;
  - „Neu berechnen“;
  - manuell planen.
- Bei geändertem Datenstand/409 wird Vorschlag als veraltet markiert und neu berechnet.
- Keine automatische Fixierung oder Benachrichtigung.

### Tests und Evaluation

- Unit: Gates, Gewichtung, Tie-Breaker, Wunschzeitraum, Lücken, fehlende Koordinaten, Transport, Gruppen.
- Property-/fuzz-artige Tests: Vorschlag überschneidet niemals bekannte Reservierung; Score bleibt begrenzt; Dauer vollständig.
- Integration: realistische Termine/Availability/DB-Versionen, Übernahme mit Revalidierung und Race.
- Routingadapter: Timeout, ungültige Antwort, Größenlimit, Circuit/Backoff, Fallback, kein PII-Payload.
- Golden/Scenario Tests mit Huber/Maier/Berger und erwarteter Rangfolge/Gründen, ohne fragile exakte Gleitkommazahlen.
- Performancebudget für z. B. 100 Wartelisteneinträge, 6 Fahrer, 5 Ressourcen und 8-Wochen-Horizont; dokumentiere Ziel und Messung.

## Verbindliche Regeln

- Engine schlägt vor, fixiert nie.
- Übernahme validiert alles erneut und erstellt höchstens Proposal.
- Harte Konflikte werden nie durch hohen Score überstimmt.
- Erklärbarkeit ist Teil des Outputs.
- Routingproviderziel nur aus Startkonfiguration; kein SSRF.
- Keine Kundennamen/PII an Distanzprovider.
- Weitere Maschinen/Fahrer funktionieren ohne Algorithmus-Neubau.

## Nicht Bestandteil

- globale mathematische Tourenoptimierung/VRP mit Garantie;
- autonome Terminfixierung;
- Live-Verkehrsgarantie;
- automatische Geocodierung ohne Review;
- KI/LLM als Kernentscheidungsinstanz.

## Akzeptanzkriterien

- [ ] Für einen planbaren Auftrag werden bis zu drei konfliktfreie Vorschläge mit Gründen erzeugt.
- [ ] Wunschzeitraum, Dauer, Fahrer, Ressourcen, Transport und bestehende Termine wirken nachweisbar ein.
- [ ] Distanz nutzt Routingadapter oder klar gekennzeichneten Haversine-Fallback.
- [ ] Fehlende Koordinaten werden verständlich behandelt.
- [ ] Identischer Input liefert deterministische Rangfolge.
- [ ] Übernehmen erzeugt nur Proposal und revalidiert Version/Konflikte.
- [ ] Geografische Clusterhinweise funktionieren ohne automatische Buchung.
- [ ] Provider erhält keine PII und ist gegen SSRF/Timeout/überlange Antwort abgesichert.
- [ ] Performancebudget ist definiert und mit realistischem Datensatz geprüft.
- [ ] `acceptance/planning.feature` ist automatisiert.

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
make check
```

Führe zusätzlich deterministische Szenario-/Performancechecks und einen Race-Test beim Übernehmen eines inzwischen belegten Vorschlags aus.

## Abschlussbericht

Dokumentiere Gates, Scoreformel/Gewichte, Tie-Breaker, Routing-/Fallbackstrategie, Performance und Beispiele der erklärten Top-3. Belege, dass kein Pfad selbstständig fixiert oder benachrichtigt.

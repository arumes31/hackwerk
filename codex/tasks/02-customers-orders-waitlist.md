# Task 02 – Kundenakten, Hackaufträge, Notizen und sortierbare Warteliste

**Empfohlener Aufruf**

```text
$hackplan-implement Implementiere codex/tasks/02-customers-orders-waitlist.md vollständig.
```

## Ziel

Admin und Fahrer können Kunden erfassen, mehrere Hackaufträge pro Kunde anlegen und Aufträge in einer übersichtlichen Warteliste verwalten. Die Kundenakte zeigt Kontaktdaten, Standort, Auftragshistorie und Notizen. Noch findet keine Terminplanung statt.

## Vor der Implementierung lesen

- `AGENTS.md`, `PLANS.md`
- `docs/00-product-vision.md`
- `docs/03-domain-model.md`
- `docs/04-status-state-machine.md`
- `docs/05-rbac.md`
- `docs/06-ux-and-responsive.md`
- `docs/07-api-and-integrations.md` (Maps-Link)
- `docs/10-security-privacy.md`
- `reference/seed-scenario.md`
- `acceptance/core-workflow.feature`

Erstelle `docs/exec-plans/02-customers-orders-waitlist.md`.

## Scope

### Datenbank und Domain

Migrationen, Queries, Domainwerte und Services für:

- `customers` mit getrennten strukturierten Adressfeldern, `address_freeform`, Telefon roh/normalisiert, E-Mail, Benachrichtigungspräferenz, optionalen Koordinaten, Geocodingstatus, Archivierung und `version`;
- `jobs` mit fortlaufender lesbarer Auftragsnummer, Auftragstyp, m³, Hackdauer, Transportdauer/-fahrten/-modus, Wunschzeitraum/Freitext, Dringlichkeit, Region, Quelle, Workflowstatus, Archivierung und `version`;
- `waitlist_entries` mit Eintritt, manueller Priorität, Regionssnapshot und Entfernungshistorie;
- append-only `job_notes` mit Autor/Zeitstempel und optionaler klar auditierter Korrekturstrategie;
- DB-Constraints aus `docs/03-domain-model.md`, insbesondere positive Menge/Dauer, valide Zeiträume und konsistente Transportwerte.

Verwende transaktionale Services: Kunde + erster Auftrag + Wartelisteneintrag können in einem atomaren Flow angelegt werden. Auftragsnummern müssen unter Parallelität eindeutig sein.

### Kundenfunktionen

- Kundenliste mit Suche nach Name, Firma, Ort, Telefonnummer und Auftragsnummer; serverseitig paginiert.
- Kunde anlegen, bearbeiten und archivieren. Historie nicht physisch löschen.
- Doppelerfassungswarnung anhand normalisierter Telefonnummer/E-Mail und ähnlich wirkender Name+Ort-Kombination. Nur Warnung, kein unsicheres automatisches Zusammenführen.
- Kundenakte mit aktuellen/historischen Aufträgen, bisherigen Terminen als leerer vorbereiteter Abschnitt, Notizen und Auditmetadaten in angemessenem Umfang.
- Sicheren Button „In Google Maps öffnen“ serverseitig aus Koordinaten oder formatierter Adresse generieren. Keine beliebige benutzerkontrollierte URL speichern/ausgeben.
- Fehlende Koordinaten verhindern Navigation nicht; verwende die Adresse als Query.

### Auftragsfunktionen

- Auftrag an bestehendem oder neuem Kunden anlegen/bearbeiten/archivieren.
- Auftragstypen „Nur Hackmaschine“ und „Hackmaschine mit Transport“.
- Dauerfelder mit verständlicher Eingabe, intern Minuten. Unterstütze etwa `3:30`, Stunden/Minuten-Controls oder klare getrennte Felder, aber speichere kanonisch.
- Transportfelder nur bei entsprechendem Auftragstyp aktiv, serverseitig dennoch validiert.
- Wunschzeitraum mit Start/Ende plus Freitext („möglichst bald“, „Anfang September“).
- Statuswechsel in diesem Task nur Warteliste/abgebrochen bzw. vorbereitete Planning-Schnittstelle; keine Terminfixierung.
- Fahrer dürfen Kunden/Aufträge anlegen und bearbeiten, aber keine historischen Statusmanipulationen oder physischen Löschungen.

### Warteliste

- Eigene Seite mit Karten-/Tabellenansicht, responsive.
- Zeige mindestens Kunde/Firma, Ort/Region, m³, Hackdauer, Transportindikator, Wunschzeitraum, Dringlichkeit, Eingang, Alter und Bemerkungsauszug.
- Sortierung: Eingang, Wunschzeitraum, Dringlichkeit, Holzmenge, Region; auf-/absteigend.
- Filter: Auftragstyp, Region, Dringlichkeit, Wunschmonat, Volltext/Suche.
- Stabile, serverseitige Pagination und URL-Parameter, damit Ansichten teilbar/bookmarkbar sind.
- Admin kann Priorität ändern und Einträge entfernen/archivieren; Fahrer dürfen neue Einträge anlegen, aber keine fremde Priorisierung manipulieren.
- Bereite semantische Drag-Handles/Datenausgabe für Task 04 vor, ohne Drag-and-drop bereits als Terminlogik zu implementieren.
- Mobile Aktion „Einplanen“ darf als deaktivierte/vorbereitete Adminaktion sichtbar sein, aber keine falsche Funktion vortäuschen.

### HTTP/UI

- Verwende HTML-Formulare/htmx passend zur Architektur; JSON nur dort, wo später Kalenderintegration es erfordert.
- Optimistic concurrency bei Editieren (`version`); stale Update ergibt 409 mit verständlichem Reload/Compare-Hinweis.
- PRG oder htmx-konforme Redirect-/Swap-Semantik, sodass Reload keine doppelten Datensätze erzeugt.
- Formulare mit Labels, Einheiten, Hilfe, Inlinefehlern und 44px Touchzielen.
- Telefonnummern/E-Mail nur im erforderlichen internen Kontext anzeigen; keine Daten in URL-Queryparametern.

### Audit, Tests und Seed

- Audit: Kunde/Auftrag angelegt, relevante Felder geändert, archiviert, Warteliste hinzugefügt/entfernt/Priorität geändert, Notiz ergänzt. Nur Feldnamen/minimierte Werte.
- Seed Huber, Maier, Berger aus der Anforderung mit verschiedenen m³, Dauer, Region/Wunschzeitraum und Auftragstypen.
- Unit-Tests für Validierung, Transportinvarianten, Sortier-/Filtermapping und Telefonnummernormalisierung.
- Integrationstests für Transaktionen, Auftragsnummernparallelität, Archivierung, unique aktiver Wartelisteneintrag und optimistic concurrency.
- E2E für Erfassung durch Fahrer, Admin-Bearbeitung, Such-/Sortieransicht, Maps-Link und negative Rechte.

## Verbindliche Regeln

- Kunde und Auftrag niemals zu einem Datensatz verschmelzen.
- Ein Kunde darf mehrere Aufträge haben.
- Archivierte Datensätze bleiben in Historie/Audit referenzierbar.
- Region wird als planungsrelevantes Feld gespeichert, aber nicht aus unsicherer Freitextlogik automatisch überschrieben.
- Kein Geocoding-Netzwerkaufruf in diesem Task; nur Port/Status vorbereiten.
- Keine beliebigen externen URLs aus Kundeneingabe ausgeben.

## Nicht Bestandteil

- Termin-/Kalenderdaten und Fahrerzuweisung;
- echter Geocoder;
- SMS/E-Mail-Versand;
- automatische Dublettenfusion;
- Spracheingabe.

## Akzeptanzkriterien

- [ ] Admin und Fahrer können Kunde + Auftrag + Wartelisteneintrag in einem durchgängigen Flow erfassen.
- [ ] Ein Kunde besitzt mehrere getrennte Aufträge und eine nachvollziehbare Historie.
- [ ] Transportpflichten und Mengen/Dauern werden client- und serverseitig konsistent validiert.
- [ ] Warteliste ist nach allen geforderten Kriterien sortier-/filterbar und mobil nutzbar.
- [ ] Stale Edits überschreiben keine neueren Änderungen.
- [ ] Fahrer kann keine Adminpriorität oder Archivierungsregeln umgehen.
- [ ] Maps-Navigation verwendet Koordinaten oder sichere URL-kodierte Adresse.
- [ ] Notizen sind append-only und nach Autor/Zeit nachvollziehbar.
- [ ] Seed enthält die drei Beispielszenarien.
- [ ] Kritische Flows sind mit Unit-, DB- und Browsertests abgedeckt.

## Pflichtprüfungen

```bash
make generate
make format
make lint
make test
make test-integration
make test-e2e
make build
make check
```

Teste zusätzlich parallele Auftragsnummerngenerierung, XSS in allen Freitextfeldern und direkte Fahrerrequests auf Adminaktionen.

## Abschlussbericht

Liste Tabellen/Migrationen, Routen, Validierungsregeln, Archivierung, Maps-Link-Strategie und Testresultate. Zeige, dass die Domänentrennung und Erweiterbarkeit für weitere Aufträge erhalten bleibt.

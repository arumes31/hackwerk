# Task 06 – Startdashboard, responsive Navigation und operativer Tagesüberblick

**Empfohlener Aufruf**

```text
$hackplan-implement Implementiere codex/tasks/06-dashboard-mobile.md vollständig.
```

## Ziel

Nach dem Login sehen Administratoren und Fahrer einen sofort nutzbaren Tagesüberblick: heutige Termine, Wartelistenstand, ausstehende Kundenantworten, Fahrer-Verfügbarkeit, Konflikte, freie Kapazitäten und kommende Aufträge. Die gesamte Kernanwendung ist auf Smartphone, Tablet und Desktop konsistent bedienbar.

## Vor der Implementierung lesen

- `AGENTS.md`, `PLANS.md`
- `docs/00-product-vision.md`
- `docs/05-rbac.md`
- `docs/06-ux-and-responsive.md`
- `docs/10-security-privacy.md`
- `docs/11-test-strategy.md`
- `acceptance/core-workflow.feature`
- vorhandene Kunden-, Kalender-, Availability- und Notification-Module

Erstelle `docs/exec-plans/06-dashboard-mobile.md`.

## Scope

### Dashboard-Read-Model

Implementiere einen effizienten Application Query Service für den angemeldeten Benutzer und einen gewählten lokalen Tag:

- heutige Termine sortiert nach Startzeit;
- nächster Termin und Restzeit bis Start nur im Browser dynamisch, ohne fachliche Entscheidung;
- Anzahl aktiver Wartelisteneinträge;
- heutige und kommende Termine innerhalb eines begrenzten Horizonts;
- fixierte Termine mit offener/abgelehnter/Rückruf-Bestätigung;
- fehlgeschlagene oder seit konfigurierbarer Zeit ausstehende Benachrichtigungen;
- heutige Fahrer-Verfügbarkeit;
- bekannte Termin-/Availability-Konflikte und Admin-Overrides;
- freie Zeitblöcke der Hackressourcen innerhalb konfigurierter Betriebszeiten;
- Aufträge mit hohem Alter/Dringlichkeit oder bald endendem Wunschzeitraum.

Vermeide N+1-Abfragen. Nutze dedizierte SQL-Read-Queries und messe/prüfe einen realistischen Seed-Datensatz. Dashboarddaten dürfen maximal den berechtigten internen Umfang enthalten.

### Rollenabhängige Darstellung

**Admin:**

- alle Kennzahlen und Problemkarten;
- direkte Aktionen zu Warteliste, Planen, Nachricht erneut senden und Konflikt prüfen;
- Hinweis auf veraltete/fehlende Verfügbarkeiten;
- freie Kapazitäten und ungeplante dringende Aufträge.

**Fahrer:**

- alle heutigen und kommenden Termine aller Fahrer;
- eigene Verfügbarkeit prominent;
- Navigation zum Kunden, Auftrag öffnen, Notiz ergänzen und gegebenenfalls „erledigt“ markieren, falls Profilrecht erlaubt;
- keine Adminaktionen, internen Providerfehler oder vertraulichen Abwesenheitsdetails.

### Startseite und Komponenten

- Bereich „HEUTE“ mit Maschinen-/Ressourcengruppierung und Zeilen wie `08:00 Huber – 80 m³`.
- Cards/Zähler für Warteliste, Termine, offene Antworten, Konflikte, Fahrer und freie Kapazität.
- Statusbadges mit Text, Icon und Farbe.
- Empty States mit sinnvoller nächster Aktion.
- Karten sind Links/Buttons mit zugänglichen Namen; keine Click-Handler auf nicht-semantischen `div`-Elementen.
- Nutzer kann Datum vor/zurück/Heute wechseln, ohne unlimitierte Queries.
- Dashboard aktualisiert gezielt per htmx oder normalem Reload; kein aggressives Polling. Optionale Aktualisierung mindestens mit sichtbarer Aktualisierungszeit und Pause bei verborgenem Tab.

### Responsive App-Shell

Vervollständige Hauptnavigation:

- Desktop/Tablet: Sidebar oder horizontale Navigation für Dashboard, Kalender, Warteliste, Kunden, Fahrer, Planungsvorschläge, Einstellungen.
- Smartphone: kompakte Bottom-Navigation für häufige Bereiche plus Menübutton für Rest; Fokusmanagement beim Öffnen/Schließen.
- aktiver Menüpunkt, Breadcrumb/Seitentitel, Benutzer-/Logoutmenü;
- Rollen blenden nur nicht erlaubte Bereiche aus, serverseitige Gates bleiben bestehen;
- Touchziele mindestens 44×44, keine horizontale Seitenüberläufe bei 320/360 px.

### Mobile Optimierung aller bisherigen Kernseiten

Audit und verbessere:

- Login/Passwortwechsel;
- Kundenliste/-akte/-formular;
- Warteliste;
- Fahrer-Verfügbarkeit;
- Kalender und Termindetail;
- Confirmationstatus/Benachrichtigungsaktionen.

Anforderungen:

- Tabellen wechseln bei kleinen Breiten in Karten/scrollbare fachlich sinnvolle Darstellung;
- modale Dialoge werden auf Mobilgeräten zu nutzbaren Fullscreen-Sheets oder Seiten;
- virtuelle Tastatur verdeckt keine primären Aktionen;
- Datum/Zeit-Eingaben sind verständlich und serverseitig unabhängig vom Browserformat;
- Maps-Button ist mit einer Hand erreichbar;
- Drag-and-drop bleibt optional; „Einplanen“ ist vollwertig.

### Accessibility und UX-Qualität

- semantische Überschriftenstruktur, Landmarks, Skip-Link;
- sichtbarer Tastaturfokus;
- Kalenderaktionen per Tastatur bzw. alternative Formulare;
- ARIA nur ergänzend, native Elemente bevorzugen;
- Live-Regionen für htmx-/Kalendererfolg und Fehler, ohne Screenreader-Spam;
- Mindestkontrast und Status nicht nur über Farbe;
- Error Summary plus Feldfehler bei längeren Formularen;
- Zeitsprache `de-AT`, 24-Stunden-Format und korrekte Pluralisierung.

### Tests und Performance

- Browser-Tests auf repräsentativen Viewports: 360×800, Tablet und Desktop.
- Tastatur-Smoke-Test für Navigation, Dashboard, Kalenderalternative und Formulare.
- visuelle oder DOM-basierte Regressionstests für zentrale responsive Zustände, ohne fragile Pixelabhängigkeit.
- Query-/Performance-Test mit mindestens einigen hundert Kunden/Aufträgen und mehreren Monaten Kalenderdaten; Bereichsabfragen müssen begrenzt bleiben.
- negative Rollen-Tests für Dashboardaktionen.

## Verbindliche Regeln

- Fahrer sehen alle Termine, nicht nur eigene.
- Dashboard ist Read Model; es dupliziert keine Geschäftslogik und schreibt keine abgeleiteten Statuswerte zurück.
- Freie Zeit ist eine berechnete Information, kein garantierter Plan ohne Servervalidierung.
- Keine PII in Browser-Storage, Telemetrie oder URL-Query außer fachlich unkritischen Filtern.
- Kein endloses Polling oder WebSocket-Zwang in V1.

## Nicht Bestandteil

- Planungsvorschlagsalgorithmus (Task 08);
- Spracheingabe (Task 09);
- umfassende Statistik-/BI-Reports;
- native Mobile-App/PWA-Offlinebetrieb.

## Akzeptanzkriterien

- [ ] Dashboard zeigt die geforderten Tages-/Wartelisten-/Bestätigungs-/Verfügbarkeitsinformationen.
- [ ] Admin und Fahrer erhalten passende, serverseitig abgesicherte Aktionen.
- [ ] Fahrer sieht den vollständigen Terminplan aller Fahrer.
- [ ] Kernflows funktionieren bei 360 px ohne horizontales Seiten-Scrolling.
- [ ] Kalender besitzt eine vollständige nicht-dragbasierte mobile Planung für Admins.
- [ ] Status ist mit Text/Icon verständlich und tastaturbedienbar.
- [ ] Dashboardabfragen sind bereichsbegrenzt und vermeiden N+1.
- [ ] Accessibility-Smoke-Checks und responsive E2E-Tests bestehen.
- [ ] Leere, Lade- und Fehlerzustände sind für zentrale Cards implementiert.
- [ ] `acceptance/core-workflow.feature` ist bis zu diesem Meilenstein durchgängig ausführbar.

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

Führe zusätzlich einen automatisierten Accessibility-Check für zentrale Seiten und einen Dashboard-Query-Benchmark/Explain-Check aus.

## Abschlussbericht

Dokumentiere Dashboard-Read-Model, Rollenunterschiede, getestete Viewports, Accessibility-Findings, Queryverhalten und die wichtigsten UX-Verbesserungen an bestehenden Seiten.

# Review 92 – Responsive UX-, Accessibility- und Bedienfehler-Review

**Empfohlener Aufruf**

```text
$hackplan-review Prüfe die HackWerk-Oberfläche gemäß codex/tasks/92-ux-accessibility-review.md. Behebe bestätigte Blocker/High Findings und klare Accessibility-Defekte.
```

## Ziel

Prüfe die App aus Sicht eines Administrators am Desktop und eines Fahrers am Smartphone. Der Review bewertet nicht nur Optik, sondern Fehlervermeidung, Rollenverständlichkeit, Tastatur-/Screenreaderbedienung, Statusklarheit, mobile Kalenderalternative und robuste Rückmeldung bei Konflikten/Providerfehlern.

## Vor dem Review lesen

- `AGENTS.md`
- `docs/00-product-vision.md`
- `docs/05-rbac.md`
- `docs/06-ux-and-responsive.md`
- `docs/09-voice-intake.md`
- alle `acceptance/*.feature`
- aktuelle Templates, CSS, TypeScript und E2E-Tests

## Personas und Kontexte

1. **Admin am PC/Tablet:** plant viele Termine, draggt Warteliste, vergleicht Verfügbarkeiten, behebt Konflikte.
2. **Fahrer am Smartphone:** arbeitet eventuell draußen, geringe Aufmerksamkeit, öffnet Navigation, liest Auftrag, ergänzt Notiz, pflegt Verfügbarkeit.
3. **Fahrer ohne Drag-/präzise Mausbedienung:** benötigt vollständige alternative Flows.
4. **Tastaturnutzer/Screenreader:** benötigt semantische Struktur und verständliche Status-/Fehlermeldungen.
5. **Kunde am unbekannten Smartphone:** bestätigt/ablehnt ohne App/Account, mit minimalem Inhalt.

## Testmatrix

Browser soweit unterstützt:

- Chromium, Firefox, WebKit;
- 360×800, 390×844, Tablet, 1366×768/1440 Desktop;
- Touchsimulation und reine Tastatur;
- Browserzeitzone Europe/Vienna sowie abweichende Clientzeitzone;
- reduzierte Bewegung und hoher Zoom (200 %);
- deutschsprachige UI und lange Namen/Orte.

## Kernflüsse

Prüfe jeweils Happy Path, Validation, Serverfehler und Abbruch:

- Login/Passwortwechsel/Logout;
- Kunde + Auftrag + Warteliste manuell;
- Kundenakte/Maps/Notiz;
- Warteliste sortieren/filtern;
- Availabilitywoche + Urlaub;
- Kalender Tag/Woche, Heute, vor/zurück;
- Drag-and-drop Desktop;
- mobile „Einplanen“-Alternative;
- Move/Resize und 409-Revert;
- Fixieren/Benachrichtigen mit klarer irreversible/folgenreicher Aktion;
- Confirmationseite Kunde;
- Dashboard Fahrer/Admin;
- ICS-Link erzeugen/rotieren;
- Vorschlag erklären/übernehmen;
- Voice aufnehmen/review/commit/fallback.

## Accessibility-Prüfkatalog

### Struktur und Tastatur

- sinnvolle Landmarks, Überschriften, Skip-Link;
- Fokusreihenfolge, sichtbarer Fokus, keine Fokusfallen;
- Dialoge/Sheets setzen und geben Fokus korrekt zurück;
- alle Aktionen per Tastatur, einschließlich Kalenderalternative;
- kein ausschließlich hoverabhängiger Inhalt;
- Links vs Buttons semantisch korrekt;
- Touchziele mindestens 44×44 und ausreichender Abstand.

### Formulare

- jedes Feld mit Label, Einheit und verständlicher Hilfe;
- Required/Invalid nicht nur Farbe;
- Error Summary fokussierbar und mit Feldern verlinkt;
- Werte bleiben nach Fehler erhalten, außer Secrets;
- Datum/Zeit/m³/Dauer auf Mobil verständlich;
- Transportfelder dynamisch, aber serverseitige Fehler ebenfalls verständlich;
- Autocomplete-Attribute passend für Kontaktdaten/Login;
- keine blockierte Passwortmanagernutzung.

### Status und Kalender

- Badge enthält Text/Icon; Farben kontrastreich und nicht alleinige Bedeutung;
- Terminblock zeigt Zeit/Dauer/Name ohne abgeschnittene kritische Information;
- Screenreader erhält verständlichen Eventnamen und Aktionen;
- Konflikt nennt Ursache und nächste Handlung;
- Revert nach Drag/Resize visuell und per Live Region;
- freie Zeit als Hinweis, nicht als Garantie;
- Fahrer erkennt read-only Zustand ohne Frustration;
- Kunde erkennt bestätigen/ablehnen/Rückruf eindeutig, keine Dark Patterns.

### Dynamische Inhalte

- htmx-Swaps aktualisieren Seitentitel/Fokus/Live-Region sinnvoll;
- Loading/Disabled verhindert Doppelklick, aber hängt bei Fehler nicht fest;
- Offline/Timeout/Providerfehler besitzen Retry/Alternative;
- keine flackernden/pollenden Bereiche;
- `prefers-reduced-motion` respektiert;
- Voiceaufnahme zeigt aktiv, Zeit, Stop/Abbrechen und Browserfallback.

### Responsive Layout

- kein horizontaler Bodyoverflow bei 320/360 px;
- Tabellen transformieren fachlich sinnvoll;
- modale Inhalte passen mit Bildschirmtastatur;
- Navigation erreichbar mit einer Hand und Fokus;
- Karten/Buttons nicht überlagert;
- lange Namen, Firmen, Orte und Fehlermeldungen umbrechen;
- Wochenkalender mobil nicht als unlesbare 7-Spalten-Miniatur.

## Automated/Manual Evidence

- Playwright-Achsen für Viewports/Browser;
- axe oder vergleichbarer automatisierter Check als Ergänzung;
- DOM-Assertions für Fokus, Labels, Status und Overflow;
- Screenshots zentraler Zustände im Reviewbericht, sofern Tooling vorhanden;
- manueller Tastaturdurchlauf dokumentiert;
- automatisierte Tools ersetzen keine fachliche Bedienprüfung.

## Findings

Klassifiziere:

- `Blocker`: Kernflow unbenutzbar oder gefährliche Fehlbedienung wahrscheinlich;
- `High`: Nutzergruppe kann wesentliche Funktion nicht sicher ausführen;
- `Medium`: erhebliche Reibung/Accessibility-Verstoß mit Workaround;
- `Low`: Verbesserung ohne wesentliche Blockade.

Jedes Finding mit Persona, Viewport, Schritten, erwartet/beobachtet, Datei/Komponente, Fix und Regressionstest.

## Abschlusskriterien

- [ ] Kernflows auf Smartphone/Desktop und Tastatur geprüft.
- [ ] Mobile Einplanung ist vollwertig, Drag nicht Pflicht.
- [ ] Status/Fehler/Provider-/409-Zustände verständlich.
- [ ] Confirmationseite ist minimal und fehlbedienungssicher.
- [ ] automatisierte Accessibilitychecks ohne unbewertete kritische Findings.
- [ ] 200 %-Zoom/360px ohne Kernfunktionsverlust.
- [ ] Blocker/High behoben oder Releaseblocker.
- [ ] Bericht enthält positive Befunde und priorisierte Restverbesserungen.

## Abschlussbericht

Schreibe `docs/reviews/92-ux-accessibility-review.md` mit Testmatrix, Screens/Belegen, Findings/Fixes, Accessibility-Ergebnissen und Releaseempfehlung.

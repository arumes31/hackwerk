# Produktvision

## Problem

Hackaufträge entstehen heute typischerweise aus Telefonaten, Einzelnotizen, Kalendern und direkter Abstimmung. Dadurch gehen Kontext, Reihenfolge, Kundenwünsche, Fahrer-Verfügbarkeit und Bestätigungsstatus leicht auseinander. Bei einer einzigen zentralen Hackmaschine ist jede Doppelbuchung oder unnötige Leerfahrt besonders teuer.

## Vision

HackWerk verwandelt eine Kundenmeldung in einen nachvollziehbaren digitalen Ablauf:

```text
Kundenmeldung
  -> Kundenakte und Auftrag
  -> Warteliste
  -> erklärbare Terminvorschläge
  -> Admin plant und fixiert
  -> Kunde erhält Link
  -> Kunde bestätigt/ablehnt/wünscht Rückruf
  -> alle Fahrer sehen den Termin
  -> Navigation zum Einsatzort
  -> Auftrag wird durchgeführt und abgeschlossen
```

## Primäre Nutzer

### Administrator

Plant Ressourcen und Fahrer, fixiert Termine, löst Benachrichtigungen aus, reagiert auf Kundenantworten, verwaltet Benutzer und Einstellungen und trägt die Verantwortung für Konfliktfreiheit.

### Fahrer/Mitarbeiter

Erfasst Kunden und Aufträge, legt sie auf die Warteliste, sieht den gesamten Terminplan, pflegt die eigene Verfügbarkeit, öffnet Navigation, ergänzt interne Bemerkungen und darf standardmäßig einen tatsächlich erledigten Auftrag abschließen.

### Kunde ohne Benutzerkonto

Erhält eine E-Mail oder SMS mit einem sicheren Link und kann einen konkreten Termin bestätigen, ablehnen oder Rückruf anfordern. Es ist keine Installation und keine Registrierung nötig.

## Kernnutzen

- eine gemeinsame verlässliche Datenquelle;
- weniger Doppelbuchungen und Rückfragen;
- sichtbarer Bestätigungsstatus;
- bessere Nutzung freier Kapazitäten;
- weniger Leerfahrten durch geografische Vorschläge;
- schnelle Datenerfassung auch unterwegs;
- spätere Erweiterbarkeit auf weitere Maschinen, Fahrzeuge und Fahrer.

## Produktprinzipien

1. **Menschen entscheiden verbindliche Termine.** Automatik erklärt und unterstützt, übernimmt aber nicht.
2. **Sicherheit vor Bequemlichkeit.** Rechte, Konflikte und Versionen werden serverseitig geprüft.
3. **Mobile Bedienung ist gleichwertig.** Keine Kernfunktion darf ausschließlich per Maus-Drag funktionieren.
4. **Status ist eindeutig.** Auftrag, Termin, Bestätigung und Benachrichtigung haben getrennte Zustände.
5. **Historie bleibt nachvollziehbar.** Änderungen und Antworten sind auditierbar.
6. **Kleine Organisation, professionelle Basis.** Die erste Installation hat sechs Benutzer, die Architektur darf aber nicht auf sechs Nutzer oder eine Maschine hardcoden.
7. **Provider bleiben austauschbar.** SMS, E-Mail, Sprache, Geocoding und Routing werden über Adapter angebunden.

## Erfolgskennzahlen für Version 1.0

- Ein neuer Auftrag kann in höchstens zwei Minuten manuell erfasst werden.
- Der Administrator kann einen Wartelistenauftrag in weniger als einer Minute als Vorschlag einplanen.
- Jede Fixierung ist eindeutig einem Administrator und einer Version zugeordnet.
- Keine aktive Maschinen- oder Fahrerreservierung kann durch parallele Requests doppelt belegt werden.
- Ein Kunde kann ohne Anmeldung auf einem Smartphone antworten.
- Fahrer finden am Einsatztag Adresse, Ansprechpartner, Menge, Dauer, Auftragstyp, Status und Maps-Link auf einer Ansicht.
- Alle kritischen Flows sind automatisiert getestet.

# Regelbasierte Planung und Fahrstreckenoptimierung

## Ziel

Die Planungskomponente findet gute, konfliktfreie Vorschläge. Sie ist kein autonomer Disponent. Jeder Vorschlag muss erklärbar, reproduzierbar und durch den Administrator übernehmbar oder ignorierbar sein.

## Eingaben

- Auftrag und erwartete Version;
- Wunschzeitraum und Dringlichkeit;
- geschätzte Hack-/Transport-/Pufferdauer;
- notwendige Ressourcentypen;
- bekannte Kundenkoordinaten oder Region;
- bestehende Termine und Standorte;
- Fahrer-Verfügbarkeit;
- Ressourcenaktivität und Reservierungen;
- Betriebszeiten und Depotstandort;
- konfigurierbare Gewichtung.

## Harte Ausschlusskriterien

Ein Kandidat wird verworfen, wenn:

- Start/Ende außerhalb des Suchfensters oder der Betriebszeit liegt;
- kein aktiver Hackressource-Slot frei ist;
- kein zulässiger Fahrer verfügbar ist und kein ausdrücklich erlaubter Override-Modus angefordert wurde;
- Transportauftrag keinen zulässigen Transportplan besitzt;
- vorheriger/nächster Termin inklusive notwendiger Fahrzeit nicht erreichbar ist;
- Job oder relevante Termine seit Start des Runs ihre Version geändert haben;
- Zeitdauer <= 0 oder Standortdaten offensichtlich ungültig sind.

## Kandidatengenerierung

1. Suchbereich auf maximal konfigurierbare Tage begrenzen, Default 90.
2. Arbeitstage und lokale Betriebsintervalle in Europe/Vienna erzeugen.
3. Startpunkte im 15-Minuten-Raster bilden.
4. Dauer berechnen: Hack + Transport + konfigurierter Puffer.
5. freie Ressource/Fahrer abfragen.
6. benachbarte Termine desselben Tages bestimmen.
7. Fahrzeitmatrix laden oder Haversine-Fallback nutzen.
8. harte Kriterien prüfen.
9. Score berechnen.
10. nahe identische Kandidaten deduplizieren und Top-Ergebnisse liefern.

## Fahrzeit

### Providerpfad

`Router.Matrix` liefert Fahrzeit und Distanz zwischen Depot, Kandidat und benachbarten Aufträgen. Ergebnisse werden nach Koordinatenpaar und Provider-Version gecacht.

### Fallback

Bei fehlendem Provider:

- Haversine-Luftlinie;
- konfigurierbarer Straßenfaktor, z. B. 1,30;
- konfigurierbare Durchschnittsgeschwindigkeit;
- Mindestpuffer;
- Ergebnis als Schätzung markieren.

Fallback darf keine scheinpräzisen Minuten ohne Warnung anzeigen.

## Score 0–100

Die Defaultgewichte sind konfigurierbar und werden normalisiert.

| Komponente | Gewicht | Beispiel |
|---|---:|---|
| Wunschzeitraum/Präferenz | 25 | innerhalb idealem Fenster, „möglichst bald“ |
| Fahrstreckenmehrkosten | 25 | geringe zusätzliche Kilometer/Fahrzeit |
| Fahrer-Fit | 15 | vollständig verfügbar, keine knappe Grenze |
| Ressource/Transport-Fit | 10 | passende Maschine/Fahrzeug ohne Override |
| Lückenfüllung/Auslastung | 10 | schließt nutzbare Lücke statt Fragmentierung |
| Wartedauer/Dringlichkeit | 10 | älter/urgent wird bevorzugt |
| Regionale Bündelung | 5 | gleiche Region wie benachbarte Termine |

Harte Kriterien bekommen keinen Score, sondern schließen aus.

## Erklärung

Jeder Kandidat enthält:

```json
{
  "score": 91.5,
  "reasons": [
    "vollständig im gewünschten Zeitraum",
    "Fahrer Anna verfügbar",
    "Hackmaschine 1 frei",
    "nur 12 Minuten zusätzliche Fahrzeit",
    "füllt eine bestehende Vormittagslücke"
  ],
  "warnings": [
    "Fahrzeit basiert auf Luftlinien-Schätzung"
  ],
  "components": {
    "preference": 24,
    "travel": 23,
    "driver": 15,
    "resource": 10,
    "utilization": 9,
    "urgency": 7,
    "region": 3.5
  }
}
```

## Übernahme

„Vorschlag übernehmen“ sendet Kandidaten-ID, Run-ID und Inputversionen. Der Server berechnet harte Kriterien erneut und erstellt einen `proposal`. Er fixiert nicht und versendet nichts.

## Regionale Bündelung

Eine zusätzliche Übersicht kann wartende Aufträge anhand Koordinaten/Region clustern und Textvorschläge erzeugen:

> Drei offene Aufträge liegen im Raum Unterneukirchen. Gemeinsame Planung könnte die geschätzte Fahrstrecke reduzieren.

In V1 genügt ein deterministisches Grid-/Radius-Clustering. Kein ML und keine unüberprüfbare Aussage.

## Konfiguration

- Betriebszeiten je Wochentag;
- Depotkoordinaten;
- Slot-Raster;
- Suchhorizont;
- Puffer;
- Straßenfaktor/Geschwindigkeit;
- Scoregewichte;
- Mindestkonfidenz für Standort;
- maximale Providerrequests pro Run.

## Tests

- keine Überschneidung;
- Fahrer-Ausnahme schlägt Wochenregel;
- Transportressource fehlt;
- Kandidat zwischen zwei Terminen nur mit Fahrzeit möglich/unmöglich;
- Haversine-Fallback;
- gleiche Inputs ergeben gleiche Reihenfolge;
- Version ändert sich vor Übernahme -> 409;
- DST-Start und DST-Ende in Europe/Vienna;
- Scorebegründung summiert nachvollziehbar;
- kein Kandidat wird automatisch fixiert.

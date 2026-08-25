# Reproduzierbares Development-Seed-Szenario

## Benutzer/Fahrer

- `admin` – Administrator;
- `anna` – Fahrer, Montag/Dienstag/Donnerstag 08:00–17:00;
- `bernd` – Fahrer, Montag–Freitag 07:00–16:00, Mittwoch Urlaub;
- `christian` – Fahrer, Dienstag/Donnerstag 12:00–18:00;
- `doris` – Fahrer, kurzfristig Freitag nicht verfügbar;
- `emil` – Fahrer, keine Standardverfügbarkeit angelegt.

Development-Passwörter werden beim Seed in stdout nur einmal ausgegeben oder in einer lokalen Dev-Datei erzeugt, niemals im Repository festgeschrieben.

## Ressourcen

- `Hackmaschine 1`, Typ `chipper`, exklusiv;
- `Transporter 1`, Typ `transport_vehicle`, exklusiv;
- `Anhänger 1`, Typ `trailer`, exklusiv.

## Kunden/Aufträge

1. Franz Huber, Unterneukirchen 15, 80 m³, 180 Min., mit Transport, Wunsch Anfang September, Region Unterneukirchen.
2. Maria Maier, Beispielweg 4, 150 m³, 360 Min., ohne Transport, dringend.
3. Johann Berger, Waldstraße 9, 40 m³, 120 Min., ohne Transport, Wunsch Oktober.
4. Testkunde ohne Koordinaten, 60 m³, 150 Min., unvollständige Adresse für Warnung.

## Kalender

- ein bestätigter Termin am nächsten Montag 08:00–11:00;
- ein fixierter, noch unbestätigter Termin 11:30–14:00;
- ein abgelehnter Termin am Dienstag;
- ein Proposal am Donnerstag;
- eine Lücke, die der Planner sinnvoll füllen kann.

## Benachrichtigung

Der Fake-SMTP-Adapter enthält nach Fixierung eine E-Mail. Fake-SMS speichert nur Message-ID und maskierte Nummer. Der Confirmation-Link kann aus der Dev-UI geöffnet werden; E-Mail-Empfang ist nicht vorgesehen.

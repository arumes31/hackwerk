# UX- und Responsive-Spezifikation

## Navigationsstruktur

Desktop:

```text
Dashboard
Kalender
Warteliste
Kunden
Fahrer
Planungsvorschläge
Einstellungen (Admin)
```

Mobil:

- Bottom Navigation: Heute, Kalender, Warteliste, Kunden;
- Menü „Mehr“: Fahrer, Vorschläge, Einstellungen, Profil, Abmelden;
- eindeutiger Floating/Primary Action Button „Neuer Auftrag“;
- Spracheingabe als klar beschriftete Aktion, nicht nur Mikrofonicon.

## Dashboard

### Für alle

- heutige Termine chronologisch;
- Kunde/Firma, Uhrzeit, Menge, Auftragstyp, Status;
- Maps-Button;
- offene/aktuelle Bemerkungen;
- sichtbare Konflikt- oder Rückruf-Badges.

### Zusätzlich für Admin

- Anzahl Warteliste;
- unbestätigte/abgelehnte Termine;
- fehlgeschlagene Benachrichtigungen;
- Fahrer heute verfügbar/nicht verfügbar;
- erkannte Konflikte;
- freie Kapazitätsfenster der nächsten Tage;
- Link zu Planungsvorschlägen.

## Kundenakte

Abschnitte:

1. Kontaktdaten und Firma;
2. Adresse und Standortstatus;
3. direkte Aktionen Anrufen, E-Mail, Google Maps;
4. aktive Aufträge;
5. Wartelisten- und Terminstatus;
6. historische Aufträge/Termine;
7. interne Notizen und Audit-Auszug für Admin.

Auf Mobilgeräten werden Telefonnummer, E-Mail und Navigation als große Aktionsbuttons dargestellt.

## Auftragserfassung

Ein Formular oder Wizard mit:

1. Kunde suchen/neu anlegen;
2. Auftragstyp;
3. Menge und Dauer;
4. Transportdetails;
5. Wunschzeitraum/Dringlichkeit/Region;
6. Bemerkungen;
7. Zusammenfassung und „Auf Warteliste setzen“.

Dauerfelder akzeptieren Stunden/Minuten und zeigen die normalisierte Gesamtdauer. Validierungsfehler bleiben am Feld erhalten.

## Warteliste

Desktop zweispaltig neben Kalender oder als einklappbare Seitenleiste:

- Suchfeld;
- Filter Region, Wunschzeitraum, Dringlichkeit, Typ;
- Sortierung Eingang, Wunsch, Dringlichkeit, Menge, Region;
- Karten/Zeilen mit Kunde, m³, geschätzter Dauer, Zeitraum, Region und Status;
- Drag Handle nur für Admin;
- sichtbarer Button „Einplanen“ als Alternative.

Mobil:

- Liste mit Sticky Filterbar;
- Detail-Bottom-Sheet;
- „Einplanen“ öffnet Datum/Uhrzeit/Fahrer/Ressourcen-Dialog;
- kein Zwang zu Drag-and-drop.

## Kalender

### Desktop

- `TAG | WOCHE`;
- Heute und vor/zurück;
- Zeitskala in 15-Minuten-Schritten;
- Eventhöhe entspricht Dauer;
- Statusbadge, Kunde, m³ und Auftragstyp;
- Warteliste optional als linke Seitenleiste;
- Admin: Drag, Resize, externe Drags;
- Fahrer: identische Events, aber read-only.

### Mobil

- Standard `TAG` als Zeitraster;
- `WOCHE` als chronologische Wochenagenda mit Tagesüberschriften;
- Eventdetails als Bottom-Sheet;
- große Pfeile und Heute-Button;
- Admin-Änderung über „Verschieben“/„Dauer ändern“ im Dialog statt fehleranfälligem Finger-Resize.

### Drag-Verhalten

1. Externer Wartelisten-Drag löst `eventReceive` aus.
2. Browser legt ein temporäres Ghost-Event an.
3. Server erstellt nur einen Draft und prüft Version, Dauer und Rechte.
4. Bei Erfolg öffnet sich der Planungsdialog.
5. Bei Fehler wird das Event entfernt/zurückgesetzt und eine verständliche Meldung gezeigt.
6. Fixierung ist eine separate Bestätigungsaktion.

Bei Move/Resize wird bei HTTP 409 `revert()` ausgeführt und der aktuelle Termin neu geladen.

## Termin-Dialog

- Auftrag und Kundendaten;
- Start, Ende, Dauerkomponenten;
- Fahrer und Ressourcen;
- Fahrer-Verfügbarkeitsindikator;
- Transportplan;
- Konflikte mit Schweregrad und Erklärung;
- Vorschlags-/Bestätigungsstatus;
- Admin-Aktionen: Entwurf speichern, fixieren & verständigen, verschieben, absagen;
- Fahrer-Aktionen: ansehen, Maps, Bemerkung, erledigt markieren falls erlaubt.

Die Aktion „Fixieren & verständigen“ zeigt vor dem POST eine Zusammenfassung des Empfängers, der Kanäle und des Kundenzeitpunkts.

## Planungsvorschläge

Jede Vorschlagskarte zeigt:

- Datum und Uhrzeit;
- geplante Dauer;
- verfügbare Fahrer/Ressourcen;
- vorheriger/nächster Termin;
- geschätzte Zusatzfahrzeit;
- Score und verständliche Gründe;
- Warnungen bei Fallback-Distanz;
- „Vorschlag übernehmen“ und „Im Kalender ansehen“.

Kein geheimnisvoller AI-Score ohne Aufschlüsselung.

## Spracheingabe

1. Button „Neuen Auftrag per Sprache erfassen“;
2. klare Aufnahmezustände Start, Aufnahme, Stop, Upload, Verarbeitung;
3. maximale Dauer und Datenschutzhinweis;
4. Transkript sichtbar;
5. erkannte Felder als editierbares Formular;
6. niedrige Konfidenz gelb markieren;
7. fehlende Pflichtfelder rot/neutral markieren;
8. explizite Buttons „Als Entwurf verwerfen“ und „Kunde & Auftrag speichern“;
9. Speichern führt standardmäßig auf Warteliste, niemals direkt in fixierten Kalender.

## Barrierefreiheit

- semantische Überschriften und Landmarken;
- echte Buttons/Links statt klickbarer Divs;
- sichtbarer Fokus;
- Tastaturbedienung für Kalenderalternativen;
- Labels, `aria-describedby` und Fehlerzusammenfassungen;
- Status mit Text plus Farbe;
- Touch-Ziele mindestens 44×44;
- ausreichender Kontrast;
- respektiert `prefers-reduced-motion`;
- Dialog-Fokusfalle und Rückgabe des Fokus;
- Live-Region für Speichern/Fehler ohne exzessive Ansagen.

# Entscheidungen und Annahmen

Diese Datei verhindert, dass Codex bei jedem Task dieselben offenen Punkte anders entscheidet.

## Festgelegte Defaults

1. **Produktname:** `HackWerk`, UI-Name über `APP_NAME` konfigurierbar. Das Runtime-Binary heißt `hackwerk`; bestehende interne Datenbank-, Cookie- und Modulbezeichner bleiben aus Kompatibilitätsgründen vorerst unverändert.
2. **Mandanten:** eine Organisation/Installation; kein Multi-Tenant-SaaS in 1.0.
3. **Sprache/Zeitzone:** `de-AT`, `Europe/Vienna`, 24-Stunden-Zeit.
4. **Benutzer:** initial sechs, technisch unbegrenzt im normalen kleinen Betriebsrahmen.
5. **Rollen:** genau Admin und Fahrer in 1.0; Permission-Checks intern als Fähigkeiten strukturieren.
6. **Fahrerabschluss:** Fahrer darf mit Profilflag standardmäßig einen gestarteten Termin als erledigt markieren; Admin kann immer korrigieren/überschreiben.
7. **Kundenlöschung:** UI „Löschen“ archiviert. Endgültige Löschung/Anonymisierung ist ein separater Adminprozess.
8. **Kalenderfixierung:** ausschließlich Admin; Drag-and-drop allein fixiert nie.
9. **Abgelehnter Termin:** bleibt bis Adminaktion reserviert, damit keine stille Doppelbelegung entsteht.
10. **Neuer Zeitpunkt eines fixierten Termins:** alte Kundenbestätigung und Tokens werden widerrufen; neue Benachrichtigung erforderlich.
11. **Transport:** ein Transportauftrag benötigt vor Fixierung interne Transportressource oder bestätigten externen Transport mit Begründung.
12. **Dauer V1:** gesamter geplante Block = Hackdauer + Transportdauer + Puffer. Segmentierte Reise-/Arbeitsphasen sind später erweiterbar.
13. **Ressourcen:** generische Tabelle; initial `Hackmaschine 1`. Keine hardcodierte Singleton-ID.
14. **Benachrichtigung:** Ausgehende E-Mail nutzt extern konfiguriertes SMTP; E-Mail-Empfang und ein lokaler Maildienst sind nicht vorgesehen. SMS läuft über einen signierten konfigurierbaren Webhook plus Fake/Log-Adapter.
15. **Kundenlink:** kein Kundenkonto, Capability Token, drei Antwortaktionen.
16. **Kalenderintegration:** ICS-Download und Abonnement, read-only. Keine direkte bidirektionale API in 1.0.
17. **Planung:** regelbasiert, deterministisch, Top 3, erklärbar; keine autonome Fixierung.
18. **Routing:** optionaler Provider plus Haversine-Fallback.
19. **Geocoding:** optionaler Provider; Maps-Navigation funktioniert auch mit Adresse. Ungeklärte Standorte werden markiert.
20. **Sprache:** optionaler externer Provider, Rule-Parser als Fallback; Audio nicht dauerhaft speichern.
21. **Frontend:** serverseitig mit templ/htmx; FullCalendar und kleine browsernative JavaScript-Module für komplexe Interaktionen. Kein Node, npm oder Frontend-Buildschritt; Bibliotheken liegen geprüft und fest versioniert im Repository.
22. **Datenbank:** PostgreSQL als einziges persistentes System; kein Redis in 1.0.
23. **Nebenwirkungen:** Transactional Outbox und separater Worker.
24. **Mobile:** alle Kernflüsse ohne Drag-and-drop bedienbar.
25. **Offline:** keine Offline-Schreibsynchronisierung in 1.0.

## Annahmen, die im UI konfigurierbar werden

- Betriebszeiten;
- Standardpuffer;
- Depotstandort;
- Suchhorizont und Scoregewichte;
- Fahrer darf abschließen;
- Nachrichtentemplates;
- aktive Kanäle;
- Token-/Session-Laufzeiten;
- Aufbewahrungsfristen;
- Provideraktivierung.

## Punkte für eine spätere fachliche Entscheidung

Diese blockieren die technische Version 1.0 nicht und erhalten sichere Defaults:

- konkreter SMS-Anbieter;
- ob alle Fahrer die genaue Abwesenheitsbegründung sehen oder nur „nicht verfügbar“;
- ob ein fixierter, bestätigter Termin nach Verschiebung automatisch neu versendet oder vor Versand nochmals separat freigegeben wird;
- maximale tägliche Betriebsdauer;
- ob Anfahrt als eigener sichtbarer Kalenderblock modelliert wird;
- ob externe Transporte eigene Ansprechpartner/Firmen erhalten;
- gewünschte Datenaufbewahrungs- und Löschfristen;
- Branding, Logo und endgültiger Produktname.

# Backlog nach Zielversion 1.0

## Zweck

Der V1-Build bleibt bewusst auf den vollständigen digitalen Auftragsfluss fokussiert. Dieses Backlog verhindert, dass naheliegende Erweiterungen als halbfertige TODOs in den Kerncode geraten.

## Priorisierungsvorschlag

### P1 – Direkt nach Pilotbetrieb bewerten

- Ist-Zeiten, tatsächliche m³ und Transportfahrten erfassen;
- Erinnerungsbenachrichtigungen;
- Maschinenwartung/Sperrzeiten;
- Geocoderadapter mit Reviewstatus;
- bessere Karten-/Clusteransicht;
- zusätzliche Rollen „Büro/Disposition“;
- CSV-/PDF-Auswertungen mit Datenschutzprüfung;
- Betriebsstunden-/Auslastungsreport.

### P2 – Nach stabiler Nutzung

- Reise-/Hack-/Transportsegmente je Termin;
- digitale Lieferscheine/Unterschrift;
- externe Transporteure;
- qualifikationsbasierte Fahrerplanung;
- PWA-Offline-Entwürfe;
- Wetter-/Verkehrshinweise;
- Erinnerungs- und Eskalationsregeln.

### P3 – Eigene Architektur-/Securityphase

- bidirektionale Google-/Microsoft-Synchronisierung;
- Kundenportal;
- Multi-Tenant;
- ERP-/Rechnungsintegration;
- globale Optimierung/VRP;
- native Apps/Push.

## Technische Erweiterungspunkte, die V1 vorbereitet

- generische `resources` statt Einzelmaschine;
- Providerports für SMS, E-Mail, Routing, Geocoding und Speech;
- Transactional Outbox für neue asynchrone Integrationen;
- separate Job-/Appointmentobjekte für Wiederholungen/Historie;
- versionierte Templates, Events und Outboxpayloads;
- optionale spätere Terminsegmente;
- Feedfilter/Tokenrotation;
- injizierbare Clock und deterministische Planning Engine.

## Regel für neue Features

Jede größere Erweiterung benötigt:

1. konkrete Benutzerreise und Nicht-Ziele;
2. Daten-/Statusänderung;
3. RBAC-/Privacyentscheidung;
4. ADR bei Architekturänderung;
5. Migration-/Rollbackplan;
6. Unit-, DB- und E2E-Akzeptanz;
7. Betriebs-/Monitoringfolgen.

Keine Zukunftsidee darf still durch zusätzliche Spalten oder Providerlogik in V1 eingeschoben werden.

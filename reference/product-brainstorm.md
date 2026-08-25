# Produkt-Brainstorming und Erweiterungslandkarte

Dieses Dokument sammelt Ideen, ohne den V1-Scope aufzublähen. Verbindlich sind die Entscheidungen in `docs/`; hier genannte Zukunftsideen werden erst nach eigener Priorisierung und ADR umgesetzt.

## Arbeitsname und Namensideen

**Entscheidung:** `HackWerk`

Der Name ist markant, deutschsprachig und nicht auf Transport oder genau eine Maschine begrenzt. `APP_NAME` hält Branding konfigurierbar.

Weitere Richtungen:

- HackTakt
- HackDispo
- HackRoute
- HolzTakt
- ForstSlot
- ForstDispo
- HackFlow
- SchnitzelPlan
- HolzLogistik
- WaldWerk Planer

Vor einer öffentlichen Veröffentlichung sind Marken-, Domain- und App-Store-Prüfungen separat erforderlich.

## Bewusst aufgelöste Standardentscheidungen

Damit Codex ohne wiederholte Rückfragen arbeiten kann, gelten bis zu einer bewussten Änderung:

- UI Deutsch (`de-AT`), Zeitzone `Europe/Vienna`;
- modularer Monolith statt Microservices;
- PostgreSQL als einziges System of Record;
- serverseitig gerenderte UI mit dünnem JavaScript;
- lokale Accounts mit Admin/Fahrer, kein öffentlicher Signup;
- Fahrer sehen alle Termine;
- genau ein initialer Chipper-Datensatz, aber generisches Ressourcenmodell;
- Drag erzeugt Proposal, Fixierung bleibt separate Adminaktion;
- Ablehnung reserviert weiter bis zur Adminentscheidung;
- ICS read-only;
- E-Mail-Versand via externem SMTP und SMS via providerneutralem Webhook; kein E-Mail-Empfang;
- Routing optional, Haversine-Fallback;
- Sprache optional, Audio nicht dauerhaft gespeichert;
- regelbasierte Planung statt autonomem KI-Agenten.

## Sinnvolle V1.1-Ideen

### Operativer Abschluss

- digitale Arbeits-/Lieferscheine;
- tatsächliche Start-/Endzeit und Ist-Dauer;
- tatsächliche m³ und Transportfahrten;
- Kundenunterschrift am Smartphone;
- Fotos vor/nach Auftrag mit Retention und Rechtekonzept;
- Abschlussbericht per E-Mail;
- Gründe für Abweichungen zwischen Plan und Ist.

### Maschinenbetrieb

- Betriebsstunden, Tankungen, Verbrauch;
- Wartungsintervalle und Sperrzeiten;
- Störungsmeldungen, Ersatzmaschine;
- Checklisten vor Fahrt/Hacken;
- Zubehör/Anbaugeräte als Ressourcen;
- Kapazität und Maschinenqualifikation pro Fahrer.

### Kommunikation

- Nachrichtenvorlagen pro Auftragstyp;
- Erinnerung 24/48 Stunden vor Termin;
- interne Benachrichtigung bei Kundenantwort;
- Rückrufliste;
- zweisprachige Kundennachrichten;
- providerbezogene Delivery Receipts, ohne sie mit Kundenbestätigung zu verwechseln.

### Planung

- explizite Reise-/Hack-/Transportsegmente;
- Mittagspausen und Betriebsfenster;
- Wetterwarnungen als Hinweis;
- geplante Straßensperren/Verkehr als Hinweis;
- qualifikationsbasierte Fahrerzuweisung;
- mehrere Depots und Start-/Endstandorte;
- externe Frächter/Transportpartner mit Verfügbarkeit;
- manuelle „Terminsperren“ für Werkstatt/Privattermine.

## V2-Ideen

- mehrere Betriebe/Mandanten nur mit eigener Sicherheits-/Datenisolationsarchitektur;
- Kundenportal für eigene Aufträge und Dokumente;
- PWA/Offline-Entwurf für schlechte Netzabdeckung;
- native Pushbenachrichtigungen;
- bidirektionale Google-/Microsoft-Kalendersynchronisierung via OAuth;
- CalDAV;
- ERP/Buchhaltung/Rechnungsübergabe;
- Angebots- und Preisberechnung;
- GIS-Karte mit Auftragsclustern;
- mathematische Vehicle Routing/Job Shop Optimization mit menschlicher Freigabe;
- Auswertungen: m³, Maschinenstunden, Fahrkilometer, Auslastung, Plan-/Ist, Regionen;
- Rollen jenseits Admin/Fahrer, z. B. Disposition, Büro, externer Transporteur;
- Mandantenfähige Provider-/Brandingkonfiguration.

## Produktmetriken, die später nützlich sind

Nur aggregiert und datensparsam messen:

- mittlere Zeit von Wartelisteneingang bis Terminvorschlag/Fixierung;
- Bestätigungsquote und Antwortzeit je Kanal;
- Anteil verschobener/abgelehnter Termine;
- geplante vs. tatsächliche Dauer;
- Leerfahrtkilometer pro Auftrag/Monat;
- Lückenauslastung und Maschinenkapazität;
- Wartelistenalter und Einhaltung Wunschzeitraum;
- Notificationfehler und manueller Nacharbeitsaufwand.

Keine Kundennamen/Telefonnummern als Metriklabels.

## Nicht blockierende Fragen vor echtem Go-live

Die Implementierung verwendet dokumentierte Defaults; der Betreiber sollte vor Produktion dennoch entscheiden:

1. Offizielle Firma, Absendername, Telefonnummer und Datenschutzkontakt.
2. Gewünschter SMS-Provider bzw. Webhookvertrag und Kostenlimit.
3. SMTP-Server, Absenderdomain und Zustellbarkeitskonfiguration.
4. Definition der normalen Betriebszeiten, Pausen und Standardpuffer.
5. Welche Fahrer dürfen Aufträge als erledigt markieren?
6. Wird interner Transport immer mit einem konkreten Fahrzeug geplant oder teilweise extern?
7. Welche Confirmation-/Feed-/Audit-Retention ist betrieblich und rechtlich passend?
8. Soll ein abgelehnter Termin Ressourcen automatisch nur als Warnung statt Reservierung halten? V1 hält reserviert.
9. Soll Geocoding über einen eigenen/externen Dienst erfolgen und in welcher Region?
10. Welche Backup-RPO/RTO- und Offsite-Anforderungen gelten?

Diese Fragen blockieren den lokalen V1-Build nicht. Sie werden über Konfiguration oder spätere, explizite Produktentscheidungen aufgelöst.

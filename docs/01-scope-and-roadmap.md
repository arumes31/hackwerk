# Umfang und Roadmap

## Release 1.0 – verbindlicher Umfang

### Fundament

- Docker-Compose-Entwicklung und produktionsfähiges Multi-Stage-Image;
- PostgreSQL, Migrationen, Backups und Restore-Dokumentation;
- lokale Benutzer, Rollen, sichere Sessions, Audit;
- deutschsprachige responsive Oberfläche.

### Kunden und Aufträge

- Kundenakte mit Person/Firma, Adresse, Telefon, E-Mail, Standort, Historie;
- mehrere Aufträge je Kunde;
- Auftragstyp Hackmaschine oder Hackmaschine mit Transport;
- Holzmenge, Hackdauer, Transportdauer, Transporte, Wunschzeitraum, Dringlichkeit, Region, Bemerkungen;
- Archivieren statt verlustbehaftetem Löschen.

### Warteliste

- eigener Bereich mit Eingang, Wunschzeitraum, Dringlichkeit, Menge und Region;
- Sortierung, Filterung, Suche und stabile Reihenfolge;
- Desktop-Drag in Kalender;
- mobile Aktion „Einplanen“.

### Kalender

- Tag und Woche;
- Heute, vor/zurück, sichtbare Dauer und Status;
- Admin kann Entwürfe erstellen, verschieben und skalieren;
- Konfliktprüfung für Ressourcen und Fahrer;
- alle Fahrer sehen alle Termine read-only;
- Terminfixierung ausschließlich durch Admin.

### Fahrer und Ressourcen

- Fahrerprofile mit optionalem Benutzerlink;
- wiederkehrende Wochenverfügbarkeit;
- Ausnahmen wie Urlaub, Krankenstand, Abwesenheit und kurzfristige Einschränkung;
- generische Ressourcen mit Typen Maschine, Transportfahrzeug und Sonstiges;
- initial eine aktive Hackmaschine.

### Benachrichtigung und Kundenantwort

- ausgehende E-Mail über externes SMTP; kein E-Mail-Empfang;
- SMS über konfigurierbaren, signierten Webhook-Adapter plus Development-Log-Adapter;
- Transactional Outbox und Worker;
- sichere Antwortseite mit Bestätigen, Ablehnen, Rückruf;
- sichtbarer Versand- und Antwortstatus;
- erneutes Versenden und Token-Widerruf durch Admin.

### Kalenderfeeds

- ICS-Download für einen Zeitraum;
- privater, widerrufbarer Abonnement-Link pro Benutzer;
- stabile UIDs und Sequenzen für Änderungen;
- UTC-Zeitwerte, korrekte Anzeige in Europe/Vienna;
- keine bidirektionale Synchronisierung in 1.0.

### Planungsvorschläge

- deterministische Slot-Suche;
- Berücksichtigung von Wunschzeitraum, Dauer, Fahrer, Ressourcen, Transport, bestehenden Terminen, Fahrzeit und regionaler Nähe;
- Top-3-Ergebnisse mit erklärbarem Score;
- Haversine-Fallback ohne externen Routingdienst;
- optionaler Routing-/Geocoding-Adapter;
- „Vorschlag übernehmen“ erzeugt nur einen Terminentwurf.

### Spracheingabe

- Audioaufnahme im Browser;
- optionaler OpenAI-Transkriptionsadapter sowie deaktivierter/Fake-Adapter;
- deterministischer Parser plus optionaler strukturierter Extraktionsadapter;
- Ergebnis als Entwurf mit Konfidenz und markierten Unsicherheiten;
- manuelle Kontrolle und explizites Speichern;
- keine standardmäßige Speicherung des Audios.

### Betrieb

- Healthchecks, strukturierte Logs, Metriken;
- Rate Limits, Security Header, CSRF, Uploadlimits;
- CI für Format, Lint, Tests, Generierung, Vulnerability Scan und Container-Build;
- Seed-Szenario und Release-Checkliste.

## Bewusst außerhalb von Release 1.0

- Rechnungslegung, Preise, Zahlungen und Buchhaltung;
- Kundenportal mit eigenem Login;
- native Android-/iOS-App;
- echte Offline-Synchronisierung;
- Live-GPS oder Fahrzeugtelematik;
- automatische autonome Terminfixierung;
- bidirektionales Google-/Microsoft-Kalenderschreiben;
- vollwertige Tourenoptimierung mit mehreren Depots und Flotten;
- Anhänge, Fotos und Dokumentenmanagement;
- Multi-Tenancy/SaaS-Abrechnung;
- Marketing-SMS oder Newsletter.

## Spätere Erweiterungen

1. zweite oder weitere Hackmaschine und Ressourcenfilter im Kalender;
2. Wartungsfenster und Betriebsstunden der Maschinen;
3. direkte Anbieteradapter für ausgewählte SMS-Gateways;
4. Google Calendar/Microsoft Graph mit OAuth und klarer Konfliktstrategie;
5. Statistiken zu m³, Dauer, Region, Auslastung und Wartezeit;
6. CSV-Import/Export und ERP-Schnittstellen;
7. Fotos, Lieferscheine und Unterschriften;
8. Push-Benachrichtigungen/PWA;
9. optimierte Tagesrouten mit mehreren Fahrzeugen;
10. Kundenportal für Status und Dokumente.

## Reihenfolge

Die Aufgaben unter `codex/tasks` bilden den empfohlenen Pfad. Kein späteres Assistenzfeature soll den Planungskern umgehen oder vorziehen.

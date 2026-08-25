# Task 04 – Zentraler Tages-/Wochenkalender, Drag-and-drop und konfliktfreie Terminreservierung

**Empfohlener Aufruf**

```text
$hackplan-implement Implementiere codex/tasks/04-calendar-scheduling.md vollständig.
```

## Ziel

Der Administrator plant Wartelistenaufträge in einem responsiven Tages-/Wochenkalender. Desktop-Drag-and-drop sowie eine mobile „Einplanen“-Maske erzeugen Terminvorschläge. Nur Admins dürfen verschieben, Dauer ändern, Ressourcen/Fahrer zuweisen oder fixieren. Alle Fahrer sehen den vollständigen Kalender. Doppelbelegung wird server- und datenbankseitig verhindert.

## Vor der Implementierung lesen

- `AGENTS.md`, `PLANS.md`
- `docs/03-domain-model.md`
- `docs/04-status-state-machine.md`
- `docs/05-rbac.md`
- `docs/06-ux-and-responsive.md`
- `docs/07-api-and-integrations.md`
- `docs/10-security-privacy.md`
- `docs/11-test-strategy.md`
- `acceptance/calendar.feature`
- `acceptance/permissions.feature`
- `reference/status-transitions.csv`

Erstelle `docs/exec-plans/04-calendar-scheduling.md`. Behandle Datenbank-Parallelität als Hauptrisiko.

## Scope

### Datenbank und Reservierungen

Migrationen und Queries für:

- `appointments` mit Lifecycle-, Confirmation-, Zeit-, Buffer-, Fixierungs-/Abbruch-/Abschlussfeldern und `version`;
- `appointment_drivers` mit Primärfahrer und `reserved_range`;
- `appointment_resources` mit Zweck und `reserved_range`;
- partielle/geeignete PostgreSQL Exclusion Constraints gegen Überschneidung aktiver exklusiver Fahrer- und Ressourcenreservierungen;
- Constraint `ends_at > starts_at` und konsistente Range `[starts_at, ends_at)`;
- höchstens ein aktiver nicht stornierter Termin pro Auftrag;
- vorbereitete `outbox_events`-Tabelle bzw. Domain-Outboxbasis, damit Fixierung später atomar Benachrichtigungen anstoßen kann. In diesem Task genügt ein fachliches `appointment.fixed`-Event; Versand folgt in Task 05.

Entscheide sauber, wie Reservierungszeilen bei Draft/Proposal/Fixed gelten. Mindestens Proposal und Fixed blockieren exklusive Ressourcen; Draft darf entweder kurzlebig nicht blockieren oder klar dokumentiert blockieren. Keine Race-Lücke zwischen Check und Insert.

### Application Services

Implementiere explizite Use Cases:

- `CreateDraftFromWaitlist`
- `ProposeAppointment`
- `MoveAppointment`
- `ResizeAppointment`
- `AssignDriversAndResources`
- `FixAppointment`
- `CancelAppointment`
- `CompleteAppointment` (nur vorbereitete klare Rechte/Transition; volle Dashboardintegration später)
- `ListCalendarRange`
- `ListConflictsAndCapacity`

Jeder mutierende Use Case:

- prüft Adminberechtigung beziehungsweise dokumentiertes Abschlussrecht;
- validiert Job-/Terminstatus, Ressourcenart, Transportanforderung und Fahreraktivität;
- prüft Fahrer-Verfügbarkeit. Ein Admin-Override braucht Begründung und Audit;
- verwendet Transaktion und optimistic concurrency;
- übersetzt Exclusion-Constraint-Fehler deterministisch in Domainkonflikt/HTTP 409;
- aktualisiert Jobworkflow und Wartelisteneintrag konsistent;
- erzeugt minimiertes Audit/Event.

Fixierung darf in diesem Task noch keine echte Kunden-SMS/E-Mail senden. Sie erzeugt atomar den fachlichen Trigger, den Task 05 verarbeitet.

### Kalender-API

- Range-Endpunkt liefert nur angefragten begrenzten Bereich, keine unlimitierte Historie.
- Eventdaten: IDs, Titel/Name, Ort, m³, Typ, Zeit, Dauer, Lifecycle-/Confirmationstatus, Fahrer/Ressourcen, Version, erlaubte Aktionen, Maps-Link; sensible Kontakte nur in Detailansicht nach Berechtigung.
- Mutation-Endpunkte mit CSRF, Version/ETag-ähnlichem Feld, eindeutigen 400/403/404/409/422-Antworten.
- Keine Mass Assignment; Status und Actor niemals aus ungeprüftem JSON übernehmen.
- Datums-/Zeitstrings mit Offset oder klare lokale Eingabe plus serverseitige `Europe/Vienna`-Konvertierung.

### Kalenderoberfläche

Nutze lokal gebündeltes FullCalendar Standard:

- Desktop: Tages-Zeitraster und Wochen-Zeitraster, Warteliste als externe Dragquelle.
- Mobil: Tagesansicht als Default, kompakte Wochenagenda/Liste; explizite „Einplanen“-Aktion mit Datum/Uhrzeit/Dauer statt Dragpflicht.
- Controls: TAG | WOCHE, Heute, vor/zurück, Datum/Zeitraum, Filter optional Fahrer/Status.
- Zeige Dauer, Auftragstyp, m³, Ort und Status als Text/Badge/Icon.
- Freie Zeiten und Konflikte verständlich; Availability-Overlay darf zuschaltbar sein.
- Event-Klick öffnet Detailpanel/Dialog mit Kunde, Auftrag, Navigation, Fahrer, Ressourcen, Bemerkungen und erlaubten Aktionen.
- `eventDrop`/`eventResize` senden Version; bei Fehler/409 sofort `revert()` und zeigen konfliktbezogene Meldung mit Reloadoption.
- Erfolgreiches Drag aus Warteliste entfernt Eintrag erst nach Serverbestätigung aus der sichtbaren Liste.
- Drag erzeugt `draft`/`proposal`, niemals `fixed`.
- „Termin fixieren“ verlangt explizite Bestätigung und zeigt vorab Fahrer, Ressourcen, Kundenkanäle und Konflikte. Versandstatus folgt Task 05.

### Status und Verhalten

- Kombinierte Badges gemäß `docs/04-status-state-machine.md`.
- Ein abgelehnter Status ist in Task 04 noch nicht öffentlich erzeugbar, aber Rendering muss vorbereitet sein.
- Verschieben eines bereits fixierten Termins widerruft später Kundenbestätigung; implementiere Domain/Event-Semantik schon jetzt oder markiere sie als vollständig abgedeckte Erweiterung in Task 05, ohne inkonsistenten Zustand.
- Cancelled/Completed sind standardmäßig nicht editierbar.
- Fahrer-Ansicht ist read-only für Planung, darf aber Details, alle Termine, Maps-Link und Notizerfassung aus Task 02 sehen.

### Konfliktdetails

Konfliktantworten müssen nutzbar, aber datensparsam sein:

- betroffene Ressource/Fahrer;
- überschneidendes Zeitfenster;
- betroffener Terminname nur für intern berechtigte Benutzer;
- Availability-Grund;
- mögliche Aktion: anderen Slot/Fahrer/Ressource wählen oder Admin-Override nur bei Verfügbarkeit, nie bei physischer Doppelbelegung exklusiver Ressourcen.

### Tests

- Unit: Transitionen, Dauer, Transportpflicht, Verfügbarkeit, Rechte, Rangegrenzen.
- DB-Integration: Exclusion Constraints, Parallelfixierung, paralleles Move/Resize, ein aktiver Termin je Auftrag, Rollback inklusive Outbox/Audit.
- Browser: Warteliste draggen, mobile Einplanung, move/resize success und revert on conflict, Fahrer read-only, Statusanzeige.
- DST: Termine um Sommer-/Winterzeit, Tages-/Wochenbereich, lokale Darstellung.
- Property-/table-driven Tests für angrenzende `[start,end)`-Termine: 08:00–11:00 und 11:00–14:00 dürfen sich nicht überschneiden.

## Verbindliche Regeln

- Browser ist niemals alleinige Konfliktinstanz.
- Keine Doppelbelegung trotz paralleler Requests.
- Nur Admin verändert Planung; Fahrer sehen alle Termine.
- Drag-and-drop fixiert nie.
- Eine zweite Hackmaschine/Transportressource muss ohne Schemaänderung planbar sein.
- Keine Premium-FullCalendar-Funktion voraussetzen.
- Kein Nachrichtensenden im HTTP-Request.

## Nicht Bestandteil

- echter E-Mail-/SMS-Versand und Kundenantwortseite (Task 05);
- automatische Planungsvorschläge (Task 08);
- bidirektionale externe Kalendersynchronisierung;
- separate Reise-/Hack-/Transportsegmente im Kalender.

## Akzeptanzkriterien

- [ ] Kalender besitzt Tages-/Wochenansicht, Heute und Navigation auf Mobil/Desktop.
- [ ] Admin kann Wartelistenauftrag per Drag und per mobiler Maske als Vorschlag einplanen.
- [ ] Fahrer sieht alle Termine, kann aber keinen Termin per UI oder direktem Request verändern.
- [ ] Dauer wird visuell und in Reservierungen korrekt berücksichtigt.
- [ ] Ressourcen- und Fahrerüberschneidung wird durch PostgreSQL auch bei Parallelrequests verhindert.
- [ ] Availability wird geprüft; Override ist begründet/auditiert und erlaubt keine exklusive Doppelbelegung.
- [ ] Move/Resize nutzt Version und revertiert im Browser bei 409.
- [ ] Fixierung ist explizit und erzeugt atomar Status, Jobworkflow, Reservierungen, Audit und Outboxevent.
- [ ] Weitere Ressourcen funktionieren ohne Hardcoding.
- [ ] `acceptance/calendar.feature` und relevante Permission-Szenarien sind automatisiert.

## Pflichtprüfungen

```bash
make generate
make format
make lint
make test
make test-integration
make test-e2e
make test-race
make build
make check
```

Führe zusätzlich einen Parallelitätstest mit mehreren gleichzeitigen Fixierungs-/Move-Requests gegen denselben Slot aus und dokumentiere das Resultat.

## Abschlussbericht

Beschreibe Reservierungs-/Constraint-Design, Transaktionsgrenzen, HTTP-Konfliktsemantik, mobile Alternative, Statusdarstellung und Parallelitätstests. Weise explizit nach, dass ein zweites Gerät ohne Schemaänderung funktioniert.

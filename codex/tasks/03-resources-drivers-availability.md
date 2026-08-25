# Task 03 – Generische Ressourcen, Fahrerprofile und Verfügbarkeiten

**Empfohlener Aufruf**

```text
$hackplan-implement Implementiere codex/tasks/03-resources-drivers-availability.md vollständig.
```

## Ziel

Der Administrator verwaltet Fahrer und beliebig viele Maschinen/Transportressourcen. Fahrer pflegen ihre wiederkehrende Verfügbarkeit sowie einzelne Abwesenheiten. Der Administrator sieht eine verständliche Gesamtübersicht, die später direkt in Kalender und Planung einfließt.

## Vor der Implementierung lesen

- `AGENTS.md`, `PLANS.md`
- `docs/03-domain-model.md`
- `docs/05-rbac.md`
- `docs/06-ux-and-responsive.md`
- `docs/08-planning-engine.md`
- `docs/10-security-privacy.md`
- `acceptance/availability.feature`
- `reference/permissions-matrix.csv`

Erstelle `docs/exec-plans/03-resources-drivers-availability.md`.

## Scope

### Ressourcenmodell

Migrationen und Services für `resources`:

- Typen `chipper`, `transport_vehicle`, `trailer`, `other`;
- Name, Aktivstatus, Exklusivität, optionale Kapazitätsmetadaten und interne Notiz;
- keine Singleton-/Konfigurationsfelder für „die“ Hackmaschine;
- `seed-dev` legt `Hackmaschine 1` und mindestens ein beispielhaftes Transportfahrzeug an;
- Admin kann Ressourcen anlegen, bearbeiten, deaktivieren und später verwendete Ressourcen nur archivieren/deaktivieren, nicht historienzerstörend löschen;
- strukturiertes, validiertes JSON/typisierte Felder für Kapazitätsmetadaten; keine ungeprüfte JSON-Textbox als Standard-UI.

### Fahrerprofile

- Vervollständige Adminverwaltung der Fahrerprofile unabhängig vom Login.
- Fahrerprofil kann mit genau einem Benutzer verknüpft sein; Wechsel/Entkopplung wird auditiert.
- Felder: Anzeigename, Telefon, E-Mail, aktiv, `can_complete_jobs`, interne Bemerkung.
- Deaktivierung verhindert neue Zuweisung, bewahrt historische Daten.
- Ein Fahrer mit Login sieht/bearbeitet nur die eigene Verfügbarkeit; Admin kann alle verwalten.

### Wiederkehrende Verfügbarkeit

Migrationen/Domain für `availability_rules`:

- Fahrer, Wochentag (ISO 1–7), lokale Start-/Endzeit, gültig von/bis, Status `available|limited`, Notiz;
- mehrere nicht überlappende Zeitfenster pro Tag möglich;
- saubere Validierung von Endzeit, Gültigkeitsbereich und internen Überschneidungen;
- Zeitzoneninterpretation explizit `Europe/Vienna` und robuste Umrechnung für konkrete Kalenderdaten;
- API/Service liefert normalisierte Verfügbarkeitsintervalle für einen angefragten Zeitraum.

### Ausnahmen und Abwesenheiten

Migrationen/Domain für `availability_exceptions`:

- Typ `vacation`, `sick`, `unavailable`, `available_override`, `other`;
- ganztägig oder mit Start/Ende;
- wiederkehrende Regeln werden von Ausnahmen übersteuert;
- kurzfristige Verfügbarkeit kann via `available_override` ergänzt werden;
- Krankheitsdetails nicht unnötig in allgemeinen Kalenderantworten ausgeben; Fahrer sehen nur erforderliche Statusinformationen anderer Fahrer, Admin die interne Notiz nach Berechtigung;
- Konflikte/Überlappungen werden deterministisch aufgelöst und getestet.

### UI und API

- Bereich „Fahrer“ mit Verfügbarkeitsübersicht für Admin.
- Eigene Seite „Meine Verfügbarkeit“ für Fahrer, optimiert für Smartphone:
  - Wochenplan Montag–Sonntag;
  - Zeitfenster hinzufügen/entfernen;
  - Abwesenheit/kurzfristige Ausnahme eintragen;
  - klare Vorschau der nächsten Wochen.
- Admin kann Fahrer auswählen und dieselben Daten bearbeiten, inklusive Audit.
- Gesamtansicht für einen Zeitraum mit Status verfügbar/eingeschränkt/nicht verfügbar, Text + Icon, nicht nur Farbe.
- Definiere interne JSON-Endpunkte für späteren Kalender-Overlay mit Start/Ende, Fahrer-ID, Status und minimaler Notiz. Strikte Datumsbereichslimits.
- Stale Update/gleichzeitige Änderungen werden über Version/Transaktion erkannt.

### Availability Service

Implementiere einen testbaren Service:

```text
ResolveAvailability(driverID, fromUTC, toUTC) -> intervals + provenance
IsAvailable(driverID, interval) -> available/limited/unavailable + reasons
```

- Eingaben in UTC, Regeln in lokaler Zeitzone;
- DST-Sprünge und mehrdeutige lokale Zeiten explizit behandeln;
- Ergebnis nennt Quelle (Regel, Urlaub, Override), aber UI/Logs erhalten nur erlaubte Details;
- kein stillschweigendes „verfügbar“, wenn keine Regel existiert: Standard ist nicht verfügbar oder konfigurierbare klare Policy gemäß Dokumentation.

### Tests und Audit

- Unit-Tests für Auflösung, Priorität der Ausnahmen, Lücken, Überlappungen und Grenzwerte.
- Explizite DST-Tests für Wechsel in `Europe/Vienna`.
- DB-Tests für Constraints, Fahrer/User-Eindeutigkeit und konkurrierende Edits.
- E2E: Fahrer ändert eigene Woche; Fahrer kann fremde nicht ändern; Admin sieht/ändert alle; neue Ressource wird angelegt.
- Audit ohne sensible Krankheitsdetails in generischem Snapshot.

## Verbindliche Regeln

- Ressourcen generisch modellieren; keine Spalte `chipper_id` in globalen Einstellungen als einziges Gerät.
- Fahrer ohne Login bleiben planbar.
- Fahrer sehen später alle Termine, aber nicht automatisch alle vertraulichen Abwesenheitsnotizen.
- Keine Terminzuweisung in diesem Task.
- Zeiten immer mit klarer lokaler/UTC-Grenze; keine naive `time.Time`-Interpretation.

## Nicht Bestandteil

- Terminreservierungen und Exclusion Constraints für Termine (Task 04);
- automatische Dienstplanung;
- externe HR-/Urlaubssysteme;
- Pushbenachrichtigungen.

## Akzeptanzkriterien

- [ ] Seed enthält mindestens eine Hackmaschine und eine Transportressource als normale Datensätze.
- [ ] Admin kann beliebig weitere Ressourcen anlegen/deaktivieren.
- [ ] Fahrer kann ausschließlich eigene Regeln/Ausnahmen verwalten.
- [ ] Admin kann alle Verfügbarkeiten in Tages-/Wochenbereich überblicken.
- [ ] Resolver kombiniert Regeln/Ausnahmen korrekt und nennt nachvollziehbare Gründe.
- [ ] Ganztägiger Urlaub, Krankenstand, kurzfristige Nichtverfügbarkeit und Verfügbarkeits-Override funktionieren.
- [ ] DST-Grenzen in `Europe/Vienna` sind automatisiert getestet.
- [ ] Fehlende Verfügbarkeit wird nicht fälschlich als verfügbar behandelt.
- [ ] Private Abwesenheitsdetails werden nicht unnötig offengelegt oder geloggt.
- [ ] `acceptance/availability.feature` ist für diesen Scope automatisiert.

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

## Abschlussbericht

Beschreibe Ressourcen-Erweiterbarkeit, Fahrer/User-Trennung, Availability-Auflösungsalgorithmus, Zeitzonenentscheidungen und Datenschutz. Dokumentiere konkrete DST-Testfälle und negative RBAC-Tests.

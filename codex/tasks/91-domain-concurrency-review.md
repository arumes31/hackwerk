# Review 91 – Domain-, Zustands-, Transaktions- und Concurrency-Review

**Empfohlener Aufruf**

```text
$hackplan-review Prüfe Domänenlogik und Parallelität gemäß codex/tasks/91-domain-concurrency-review.md. Behebe bestätigte Blocker/High Findings mit Tests.
```

## Ziel

Prüfe unabhängig, ob Kunde, Auftrag, Warteliste, Termin, Fahrer, Ressourcen, Bestätigung und Outbox auch unter parallelen Requests konsistent bleiben. Fokus sind fachliche Invarianten, Statusübergänge, DB-Constraints, Transaktionsgrenzen, optimistic locking, DST und Idempotenz.

## Vor dem Review lesen

- `AGENTS.md`
- `docs/03-domain-model.md`
- `docs/04-status-state-machine.md`
- `docs/08-planning-engine.md`
- `reference/status-transitions.csv`
- `acceptance/calendar.feature`
- `acceptance/confirmation.feature`
- Datenbankmigrationen, sqlc-Queries und Application Services

## Arbeitsweise

- Erzeuge ein Invarianteninventar mit Implementierungsort: Domain, Service, DB-Constraint und Test.
- Identifiziere Check-then-act-Lücken und Nebenwirkungen außerhalb der Transaktion.
- Führe echte Parallelitätstests gegen PostgreSQL aus, nicht nur Mocktests.
- Prüfe sowohl Erfolg als auch Rollback/Crashpunkte.
- Dokumentiere in `docs/reviews/91-domain-concurrency-review.md`.

## Fachliche Invarianten

Prüfe mindestens:

1. Kunde, Auftrag und Termin sind getrennt; mehrere Aufträge/Historientermine bleiben möglich.
2. Pro Auftrag höchstens ein aktiver nicht stornierter Termin.
3. Aktive exklusive Fahrer-/Ressourcenreservierungen überlappen nie.
4. Angrenzende halboffene Intervalle dürfen erlaubt sein.
5. `ends_at > starts_at`; Dauer/Buffer/Transport werden korrekt berücksichtigt.
6. `chipping_only` besitzt keine widersprüchlichen Transportwerte.
7. `chipping_with_transport` kann nur fixiert werden, wenn Transport planbar oder extern bestätigt ist.
8. Draft/Proposal/Fixed/Cancelled/Completed folgen erlaubten Transitionen.
9. Nur Admin plant/moved/resized/fixed/cancelled; Completionrecht exakt nach Profil.
10. Warteliste, Jobworkflow und aktiver Termin bleiben konsistent.
11. Fixierung + Reservierungen + Confirmation/Outbox + Audit sind atomar.
12. Ablehnung storniert/entfernt Reservierung nicht automatisch.
13. Move eines fixierten Termins widerruft alte Bestätigung und plant neue atomar.
14. Completed/Cancelled werden nicht still reaktiviert.
15. Archivierung zerstört Historie nicht.
16. Weitere Maschinen funktionieren ohne Singletonannahme.

## Parallelitätsszenarien

Automatisiere/reproduziere mindestens:

- zwei Admins erstellen Proposal für denselben Auftrag;
- zwei Admins fixieren denselben Auftrag;
- zwei verschiedene Aufträge reservieren dieselbe Hackmaschine/den selben Fahrer im gleichen Slot;
- Move vs Move mit gleicher alter `version`;
- Resize vs Cancel;
- Fix vs Customer/Job Edit;
- Confirm vs Admin Move;
- Confirm vs Token Reset/Revocation;
- Worker A/B claimen dasselbe Outboxevent;
- Workercrash nach Claim und Leaseablauf;
- Retry vs manueller Resend;
- parallele Auftragsnummerngenerierung;
- Fahrer-Availability-Edit während Fixierung;
- Vorschlag übernehmen, nachdem Slot zwischenzeitlich belegt wurde;
- Feedabruf während Rotation/Widerruf.

Erwartung: genau definierte Gewinner/Verlierer, 409/Domainfehler statt silent overwrite, keine Doppelnebenwirkungen.

## Datenbankreview

- Exclusion Constraints korrekt mit `btree_gist`, Rangegrenzen und Aktivitätsbedingung;
- Foreign Keys/Delete Policies bewahren Historie;
- Unique/partial indexes für aktiven Termin, Wartelisteneintrag, Tokenversion, Idempotenz;
- Check Constraints spiegeln Kerninvarianten;
- Isolation Level/Locks bewusst und begrenzt;
- SQL-Queries enthalten Version in `WHERE` und prüfen RowsAffected;
- Deadlockreihenfolge konsistent;
- Transaktionen kurz, keine Provider-/Netzwerkaufrufe darin;
- Migrationen auf leerer und bestehender DB;
- keine Race-Lücke durch erst Reservierung löschen, dann neue außerhalb einer Transaktion.

## Outbox/Idempotenz

- Event wird mit fachlichem Commit geschrieben;
- Claiming/Lease/Retry atomar;
- stabiler Idempotenzschlüssel;
- Providererfolg vor DB-Commit transparent behandelt;
- Confirmation POST mehrfach identisch;
- Resend widerruft/rotiert Token korrekt;
- keine doppelte aktive Confirmation Request;
- Outboxpayloadversionen migrations-/workerkompatibel.

## Zeit und DST

Prüfe explizit:

- Speicherung UTC/`timestamptz`, lokale Eingabe/Anzeige `Europe/Vienna`;
- Frühjahrswechsel: nicht existierende lokale Zeiten werden abgewiesen/verständlich normalisiert;
- Herbstwechsel: mehrdeutige Zeit verlangt eindeutige Offset-/Policy;
- Tages-/Wochenrange über DST;
- Availabilityregeln an DST-Tagen;
- ICS UTC und lokale Beschreibung;
- relative Voice-/Wunschzeitangaben;
- `time.Now()` nicht unkontrolliert in Domain/Tests.

## Planungsvorschläge

- harte Gates können nicht durch Score überstimmt werden;
- Vorschlag ist keine Reservierung;
- Übernahme revalidiert Version/Slots;
- deterministische Tie-Breaker;
- vollständige Dauer;
- keine Auto-Fixierung/Outbox;
- weitere Ressourcen/Fahrer ohne Hardcoding.

## Findingformat

Wie Review 90, zusätzlich:

- verletzte Invariante;
- interleaving/Timeline der Requests;
- beobachteter DB-Zustand;
- notwendige Constraint-/Transaktions-/Teständerung.

## Abschlusskriterien

- [ ] Invariantenmatrix mit Code/DB/Test erstellt.
- [ ] Alle Parallelitätsszenarien getestet oder reproduzierbar begründet.
- [ ] Exclusion/Unique/Check/FK-Constraints geprüft.
- [ ] Outbox/Confirmation-Idempotenz nachgewiesen.
- [ ] DST-Fälle automatisiert.
- [ ] Blocker/High behoben oder Releaseblocker.
- [ ] Keine neuen langen Transaktionen/Netzwerkaufrufe in DB-Transaktionen.
- [ ] Klare Releaseempfehlung.

## Abschlussbericht

Schreibe `docs/reviews/91-domain-concurrency-review.md` mit Invariantenmatrix, Interleavings, Findings/Fixes, ausgeführten Race-/DB-/DST-Tests und Releaseempfehlung.

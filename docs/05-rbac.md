# Rollen- und Berechtigungskonzept

## Rollen

- `admin`: vollständige fachliche und administrative Rechte;
- `driver`: operative Erfassung und Einsicht, eigene Verfügbarkeit, Notizen, optional Abschluss eines Auftrags.

Die Rolle wird serverseitig aus der Session geladen. Ein ausgeblendeter Button ersetzt keine Autorisierung.

## Berechtigungsmatrix

| Fähigkeit | Admin | Fahrer |
|---|:---:|:---:|
| Dashboard sehen | ✓ | ✓ |
| alle Termine sehen | ✓ | ✓ |
| Kunden anlegen | ✓ | ✓ |
| Kunden bearbeiten | ✓ | ✓ |
| Kunden archivieren/wiederherstellen | ✓ | – |
| Aufträge anlegen/bearbeiten | ✓ | ✓ |
| Warteliste hinzufügen | ✓ | ✓ |
| Warteliste sortieren/filtern | ✓ | ✓ |
| Priorität administrativ ändern | ✓ | – |
| Terminentwurf erstellen | ✓ | – |
| Termin verschieben/Resize | ✓ | – |
| Termin fixieren | ✓ | – |
| Termin absagen/neuer Termin | ✓ | – |
| Kunde erneut benachrichtigen | ✓ | – |
| Auftrag als erledigt markieren | ✓ | konfigurierbar ✓ |
| eigene Verfügbarkeit pflegen | ✓ | ✓ |
| fremde Verfügbarkeit pflegen | ✓ | – |
| Fahrer/Ressourcen verwalten | ✓ | – |
| Benutzer verwalten | ✓ | – |
| Planungsvorschläge sehen | ✓ | optional read-only |
| Vorschlag übernehmen | ✓ | – |
| Einstellungen/Provider | ✓ | – |
| ICS-Feed für sich erzeugen/widerrufen | ✓ | ✓ |
| globale Kalenderfeeds verwalten | ✓ | – |
| Audit sehen | ✓ | – |

## Objektbezogene Regeln

- Fahrer kann nur das eigene Fahrerprofil als „me“ für Verfügbarkeitsänderungen verwenden. Eine fremde ID im Request wird ignoriert oder abgelehnt.
- Fahrer sieht alle Termine, aber keine vertraulichen Systemeinstellungen oder Providerfehler mit Secrets.
- Interne Bemerkungen sind für alle angemeldeten Mitarbeiter sichtbar; Kunden sehen sie nie auf der Bestätigungsseite oder im Nachrichtentext.
- Kundenantwort-Token gewährt ausschließlich Zugriff auf den einen Termin und die drei Antwortaktionen. Er ist keine allgemeine Kundensession.
- Kalenderfeed-Token gehört genau einem Benutzer und liefert nur den konfigurierten Scope.

## Admin-only Fixierung

Der Use Case `FixAppointment` prüft zwingend:

1. Session gültig;
2. Rolle `admin`;
3. erwartete Terminversion;
4. Auftrag aktiv und nicht archiviert;
5. Zeitraum gültig;
6. mindestens eine aktive Hackressource zugewiesen;
7. mindestens ein aktiver/verfügbarer Fahrer oder begründeter Override;
8. Transportplan bei Transportauftrag;
9. keine DB-Überschneidung;
10. Outbox und Audit in derselben Transaktion.

Keine interne API und kein Worker darf diese Regeln umgehen.

## Abschluss durch Fahrer

Als Default darf ein Fahrer mit `can_complete_jobs=true` einen fixierten Termin nach Terminbeginn als erledigt markieren. Vor Terminbeginn ist eine Begründung erforderlich oder die Aktion Admin-only. Diese Annahme ist in `docs/13-decisions-and-assumptions.md` dokumentiert und kann per Konfiguration deaktiviert werden.

## Tests

Für jede schreibende Route existieren positive und negative Berechtigungstests. Besonders zu testen:

- Fahrer ruft Admin-Endpunkt direkt auf;
- Fahrer manipuliert Fahrer-ID bei Verfügbarkeit;
- deaktivierter Benutzer nutzt alte Session;
- Kunden-Token wird für fremden Termin verwendet;
- Feed-Token wird widerrufen;
- parallel entzogenes Adminrecht während einer Session.

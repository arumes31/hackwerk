# Status- und Zustandsmodell

## Warum getrennte Zustände

Ein einzelnes Statusfeld reicht nicht aus. Ein Termin kann fixiert sein, während der Versand fehlgeschlagen ist; ein Kunde kann ablehnen, während die Ressourcenreservierung bis zur Admin-Entscheidung bestehen bleibt. Deshalb werden vier Dimensionen getrennt:

1. Auftrag-Workflow;
2. Termin-Lifecycle;
3. Kundenbestätigung;
4. Benachrichtigungsstatus.

Die Oberfläche zeigt daraus ein verständliches kombiniertes Badge.

## Auftrag-Workflow

```text
WAITLIST -> PLANNING -> SCHEDULED -> COMPLETED
    |          |            |
    +----------+------------+-> CANCELLED
```

- `WAITLIST`: aktiver Wartelisteneintrag, kein aktiver Termin;
- `PLANNING`: aktiver Draft/Proposal;
- `SCHEDULED`: aktiver fixierter Termin unabhängig von Kundenantwort;
- `COMPLETED`: Termin abgeschlossen;
- `CANCELLED`: Auftrag beendet ohne Durchführung.

Der Workflow wird durch Services konsistent mit Termin/Warteliste gehalten; kein UI schreibt ihn isoliert.

## Termin-Lifecycle

| Von | Nach | Erlaubt für | Voraussetzungen |
|---|---|---|---|
| – | `draft` | Admin | Auftrag aktiv, Zeit gültig |
| `draft` | `proposal` | Admin | Konfliktprüfung bestanden oder Override erfasst |
| `proposal` | `fixed` | Admin | Fahrer, Hackressource, Transportplan, Version und Konflikte gültig |
| `fixed` | `cancelled` | Admin | Grund erforderlich |
| `proposal` | `cancelled` | Admin | optionaler Grund |
| `fixed` | `completed` | Admin/Fahrer mit Recht | Termin gestartet oder Admin-Override |
| `completed` | – | niemand | irreversibel; Korrektur als auditierter Admin-Use-Case |
| `cancelled` | – | niemand | neuer Termin statt Reaktivierung |

Verschieben/Resize ist nur für `draft`, `proposal` und `fixed` durch Admin erlaubt. Bei einem bereits fixierten Termin wird die bestehende Kundenbestätigung ungültig, der Status geht auf `pending`, ein neuer Token wird erzeugt und eine neue Nachricht geplant.

## Bestätigungsstatus

```text
NOT_REQUESTED -> PENDING -> CONFIRMED
                         -> DECLINED
                         -> CALLBACK_REQUESTED
```

- Fixierung setzt `PENDING` und erzeugt einen aktiven Link.
- Wiederholtes identisches POST ist idempotent.
- `CONFIRMED` oder `DECLINED` kann durch denselben Link nicht beliebig umgeschaltet werden.
- `CALLBACK_REQUESTED` darf vor Ablauf einmal in bestätigt/abgelehnt wechseln.
- Admin kann Antwort zurücksetzen und neu versenden; dies widerruft alte Tokens und wird auditiert.
- Ein abgelehnter Termin bleibt reserviert und rot sichtbar, bis Admin ihn storniert oder ersetzt.

## Benachrichtigungsstatus pro Kanal

`queued -> sending -> sent`  
`queued/sending -> retry_wait -> sending`  
`retry_wait -> failed`

Ein Termin kann mehrere Notification-Zeilen besitzen. Der kombinierte Versandstatus zeigt:

- nicht geplant;
- ausstehend;
- teilweise gesendet;
- vollständig gesendet;
- fehlgeschlagen.

## UI-Status und Farben

| Anzeige | Ableitung | Farbe | Text muss sichtbar sein |
|---|---|---|---|
| Warteliste | Job `WAITLIST` | Gelb | „Warteliste“ |
| Terminvorschlag | Appointment `draft/proposal` | Orange | „Vorschlag“ |
| Fixiert, offen | `fixed` + `pending/not_requested` | Blau | „Fixiert – Antwort offen“ |
| Bestätigt | `fixed` + `confirmed` | Grün | „Kunde bestätigt“ |
| Abgelehnt/Rückruf | `fixed` + `declined/callback` | Rot | konkrete Antwort |
| Erledigt | `completed` | Dunkel/Grau | „Erledigt“ |
| Abgesagt | `cancelled` | Grau gestrichen | „Abgesagt“ |

Farbe ist niemals der einzige Signalträger.

## Fachliche Ereignisse

- `customer.created`, `customer.updated`, `customer.archived`
- `job.created`, `job.waitlisted`, `job.updated`
- `appointment.proposed`, `appointment.moved`, `appointment.fixed`
- `appointment.cancelled`, `appointment.completed`
- `confirmation.requested`, `confirmation.responded`, `confirmation.revoked`
- `notification.requested`, `notification.sent`, `notification.failed`
- `availability.changed`
- `planning.suggestion_adopted`
- `voice.draft_committed`

Ereignisnamen sind stabil und werden für Audit/Outbox verwendet, nicht als versteckte Geschäftslogik in Stringvergleichen.

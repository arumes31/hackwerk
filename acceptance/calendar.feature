@calendar @concurrency
Feature: Tages- und Wochenkalender mit sicheren Reservierungen
  Administratoren planen Termine, Fahrer sehen alle Termine und PostgreSQL verhindert Doppelbelegungen.

  Background:
    Given ein Administrator ist angemeldet
    And die Ressource "Hackmaschine 1" und die Fahrer "Franz" und "Maria" sind aktiv
    And der Auftrag "Huber" benötigt 180 Minuten Hackzeit
    And der Auftrag "Maier" benötigt 150 Minuten Hackzeit

  Scenario: Wartelistenauftrag per Drag-and-drop als Vorschlag planen
    Given der Auftrag "Huber" ist in der Warteliste
    When der Administrator ihn auf den 1. September um 08:00 zieht
    Then sendet der Browser einen versionierten Planungsrequest
    And der Server erzeugt einen Termin von 08:00 bis 11:00 im Status "Vorschlag"
    And der Termin ist nicht fixiert
    And es wurde keine Kundenbenachrichtigung versendet

  Scenario: Mobile Einplanung funktioniert ohne Drag-and-drop
    Given der Administrator verwendet einen 360 Pixel breiten Touchscreen
    And der Auftrag "Huber" ist in der Warteliste
    When er "Einplanen" auswählt
    And Datum "1. September", Uhrzeit "08:00", Fahrer "Franz" und Ressource "Hackmaschine 1" bestätigt
    Then entsteht derselbe Terminvorschlag wie beim Desktop-Drag
    And alle Pflichtfelder und Konflikte sind verständlich sichtbar

  Scenario: Angrenzende Termine sind erlaubt
    Given "Hackmaschine 1" ist am 1. September von 08:00 bis 11:00 für "Huber" reserviert
    When der Administrator "Maier" von 11:00 bis 13:30 mit derselben Ressource plant
    Then wird die Reservierung akzeptiert
    And kein Überschneidungskonflikt entsteht

  Scenario: Überlappende Ressourcenreservierung wird verhindert
    Given "Hackmaschine 1" ist am 1. September von 08:00 bis 11:00 für "Huber" reserviert
    When ein zweiter Request "Maier" von 10:30 bis 13:00 mit derselben Ressource reserviert
    Then antwortet der Server mit "409 Conflict"
    And der zweite Termin ist nicht aktiv reserviert
    And der erste Termin bleibt unverändert

  Scenario: Zwei parallele Adminrequests können dieselbe Ressource nicht doppelt buchen
    Given zwei Administrator-Sessions sehen den Slot am 1. September um 08:00 als frei
    When beide gleichzeitig verschiedene Aufträge mit "Hackmaschine 1" fixieren
    Then gewinnt genau eine Transaktion
    And die andere erhält einen fachlichen Konflikt
    And es existiert genau eine aktive Ressourcenreservierung für den überschneidenden Zeitraum
    And keine verwaiste Outbox- oder Wartelistenänderung der verlorenen Transaktion bleibt zurück

  Scenario: Stale Move wird im Browser zurückgesetzt
    Given ein Termin besitzt Version 4
    And eine andere Adminsession verschiebt ihn und erzeugt Version 5
    When der erste Browser mit Version 4 einen weiteren Move sendet
    Then antwortet der Server mit "409 Conflict"
    And FullCalendar führt "revert" aus
    And die UI zeigt eine Meldung zum Neuladen
    And die Version-5-Zeit bleibt gespeichert

  Scenario: Fahrer sieht Kalender read-only
    Given ein Fahrer ist angemeldet
    And Termine mehrerer Fahrer existieren
    When er Tages- und Wochenansicht öffnet
    Then sieht er alle Termine im angefragten Zeitraum
    And Drag, Resize, Fixieren und Absagen sind nicht verfügbar
    When er dennoch einen direkten Move-Request sendet
    Then antwortet der Server mit "403 Forbidden"

  Scenario: DST-Tag wird korrekt dargestellt
    Given ein valider Termin liegt über einem Sommer- oder Winterzeitwechsel in Europe/Vienna
    When Tages-, Wochen- und Detailansicht geladen werden
    Then entsprechen Start, Ende und Dauer der gespeicherten UTC-Zeit
    And keine Stunde wird still hinzugefügt oder entfernt
    And eine nicht existierende lokale Eingabe wird verständlich abgewiesen

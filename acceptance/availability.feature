@availability @timezone
Feature: Fahrer pflegen Verfügbarkeit und Administratoren planen dagegen
  Wiederkehrende Wochenregeln und Ausnahmen werden in Europe/Vienna korrekt kombiniert.

  Background:
    Given Fahrer "Franz" ist einem aktiven Benutzer zugeordnet
    And Fahrer "Maria" existiert ebenfalls

  Scenario: Fahrer erfasst eine normale Arbeitswoche
    Given "Franz" ist angemeldet
    When er Montag 08:00 bis 17:00 als verfügbar speichert
    And er Donnerstag 12:00 bis 17:00 als eingeschränkt speichert
    Then zeigt "Meine Verfügbarkeit" beide Zeitfenster
    And der Administrator sieht dieselben normalisierten Zeitfenster

  Scenario: Urlaub übersteuert Wochenregel
    Given "Franz" ist jeden Mittwoch von 08:00 bis 17:00 verfügbar
    When er für einen konkreten Mittwoch ganztägigen Urlaub einträgt
    Then liefert der Availability Service für diesen Mittwoch "nicht verfügbar"
    And der Ursprung ist die Urlaubsausnahme
    And eine Planung an diesem Tag erzeugt einen verständlichen Konflikt

  Scenario: Kurzfristiger Override ergänzt Verfügbarkeit
    Given für Donnerstag existiert keine normale Verfügbarkeit
    When "Franz" Donnerstag 12:00 bis 17:00 als kurzfristig verfügbar einträgt
    Then ist genau dieses Zeitfenster verfügbar
    And Zeiten davor und danach bleiben nicht verfügbar

  Scenario: Fahrer kann fremde Verfügbarkeit nicht ändern
    Given "Franz" ist angemeldet
    When er einen direkten Request sendet, um "Maria" als nicht verfügbar zu markieren
    Then antwortet der Server mit "403 Forbidden"
    And Marias Daten bleiben unverändert

  Scenario: Admin kann eine Availability-Abweichung nur begründet übersteuern
    Given ein Fahrer ist zum Terminzeitpunkt nicht verfügbar
    When der Administrator einen Terminvorschlag fixieren möchte
    Then wird die Fixierung zunächst blockiert
    When der Administrator einen gültigen Overridegrund erfasst
    Then darf die Availability-Regel übersteuert werden
    And der Override wird auditiert
    But eine Überschneidung einer exklusiven Ressource bleibt weiterhin blockiert

  Scenario: Fehlende Regeln bedeuten nicht automatisch verfügbar
    Given für einen Fahrer existieren keine Regeln oder Overrides im angefragten Zeitraum
    When die Availability abgefragt wird
    Then ist das Ergebnis "nicht verfügbar" gemäß dokumentierter Defaultpolicy
    And die Planungsoberfläche macht die fehlende Pflege sichtbar

  Scenario: Sommer- und Winterzeit werden deterministisch behandelt
    Given eine Wochenregel liegt an einem DST-Wechseltag
    When der Service konkrete UTC-Intervalle erzeugt
    Then ist die lokale Start-/Endzeit gemäß Europe/Vienna korrekt
    And nicht existierende oder mehrdeutige lokale Zeit wird nach dokumentierter Policy behandelt
    And die Dauer wird nicht still verfälscht

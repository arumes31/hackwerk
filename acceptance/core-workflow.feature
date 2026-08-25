@core @e2e
Feature: Vom Kundenanruf bis zum erledigten Hackauftrag
  Als interner Benutzer
  möchte ich einen Kunden und Auftrag durch den gesamten Ablauf führen,
  damit Warteliste, Planung, Bestätigung und Durchführung nachvollziehbar sind.

  Background:
    Given die Zeitzone der Anwendung ist "Europe/Vienna"
    And es existieren ein aktiver Administrator "Anna Admin" und ein aktiver Fahrer "Franz Fahrer"
    And die Ressource "Hackmaschine 1" ist aktiv und exklusiv

  @mvp
  Scenario: Fahrer erfasst einen neuen Kunden direkt in die Warteliste
    Given "Franz Fahrer" ist angemeldet
    When er den Kunden "Franz Huber" mit Adresse "Unterneukirchen 15", Telefon "0664 1234567" und E-Mail "franz.huber@example.test" anlegt
    And er einen Auftrag "Nur Hackmaschine" mit 80 m³, 180 Minuten Hackdauer und Wunsch "Anfang September" anlegt
    And er den Auftrag in die Warteliste aufnimmt
    Then existiert genau eine Kundenakte für "Franz Huber"
    And die Kundenakte enthält genau einen aktiven Auftrag
    And der Auftrag hat den Workflowstatus "Warteliste"
    And der Wartelisteneintrag zeigt 80 m³, 3 Stunden und den Wunschzeitraum
    And es wurde noch kein Termin angelegt

  @mvp
  Scenario: Administrator plant, fixiert und Kunde bestätigt
    Given der Auftrag von "Franz Huber" ist in der Warteliste
    And "Franz Fahrer" ist am 1. September von 08:00 bis 17:00 verfügbar
    When "Anna Admin" den Auftrag am 1. September von 08:00 bis 11:00 als Vorschlag einplant
    And sie "Franz Fahrer" und "Hackmaschine 1" zuweist
    Then ist der Termin ein Vorschlag und noch nicht fixiert
    And der Wartelisteneintrag ist nicht mehr als ungeplant dargestellt
    When "Anna Admin" die Aktion "Termin fixieren und verständigen" bestätigt
    Then ist der Termin fixiert
    And der Kundenbestätigungsstatus ist "Antwort offen"
    And eine Benachrichtigung ist in der Outbox eingereiht
    When der Worker die Nachricht erfolgreich versendet
    And der Kunde den Termin über den Link bestätigt
    Then zeigt der Kalender "Kunde bestätigt"
    And "Franz Fahrer" sieht den Termin im vollständigen Kalender
    And der Maps-Button führt zu einer Navigation für die Kundenadresse

  @mvp
  Scenario: Fahrer ergänzt eine Bemerkung und markiert einen Auftrag erledigt
    Given ein bestätigter Termin für "Franz Huber" ist heute fixiert
    And "Franz Fahrer" ist laut Fahrerprofil zum Abschließen berechtigt
    When "Franz Fahrer" die Bemerkung "Hackplatz gut erreichbar" ergänzt
    And er den Auftrag nach Terminbeginn als erledigt markiert
    Then ist die Bemerkung mit Autor und Zeitpunkt in der Kundenakte sichtbar
    And der Terminstatus ist "Erledigt"
    And der Auftragsworkflow ist "Erledigt"
    And der Termin kann vom Fahrer nicht erneut geöffnet oder verschoben werden

  Scenario: Ein Kunde kann mehrere getrennte Aufträge besitzen
    Given die Kundenakte "Franz Huber" existiert bereits
    When ein Benutzer einen zweiten Auftrag mit 120 m³ und Transport anlegt
    Then existieren zwei getrennte Aufträge mit eigenen Auftragsnummern
    And der historische erste Termin bleibt dem ersten Auftrag zugeordnet
    And eine Änderung am zweiten Auftrag verändert den ersten Auftrag nicht

  Scenario: Archivierung bewahrt die Auftragshistorie
    Given ein Kunde besitzt einen erledigten Termin
    When der Administrator den Kunden archiviert
    Then ist der Kunde nicht mehr in der Standard-Kundenliste
    But die historische Termin- und Auditzuordnung bleibt erhalten
    And ein direkter historischer interner Link zeigt einen verständlichen Archivhinweis

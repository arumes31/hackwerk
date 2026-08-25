@notification @confirmation @security
Feature: Zuverlässige Terminmitteilung und Kundenantwort ohne Konto
  Der Administrator fixiert bewusst, ein Worker versendet und der Kunde antwortet über einen sicheren Link.

  Background:
    Given ein Administrator ist angemeldet
    And ein konfliktfreier Terminvorschlag mit Kunde, Fahrer und Hackmaschine existiert
    And der Kunde besitzt einen gültigen Benachrichtigungskanal

  Scenario: Fixierung schreibt Fachzustand und Outbox atomar
    When der Administrator "Termin fixieren und verständigen" bestätigt
    Then ist der Termin fixiert
    And der Auftragsworkflow ist "Geplant"
    And der Bestätigungsstatus ist "Antwort offen"
    And eine aktive Confirmation Request existiert
    And mindestens ein passendes Notification-Outboxevent existiert
    And der Roh-Token ist nicht in der Datenbank, im Audit oder im technischen Log gespeichert

  Scenario: Provider-Ausfall verliert die Fixierung nicht
    Given SMTP oder SMS-Webhook ist nicht erreichbar
    When der Worker das Notificationevent verarbeitet
    Then bleibt der Termin fixiert
    And die Notification wechselt nach Retryregeln in "Warten" oder "Fehlgeschlagen"
    And der Administrator sieht den Fehler ohne Roh-Providerdetails oder PII
    And das Event kann sicher erneut eingereiht werden

  Scenario: Kunde bestätigt idempotent
    Given der Kunde hat einen gültigen aktiven Bestätigungslink
    When er "Termin bestätigen" absendet
    Then ist der Bestätigungsstatus "Bestätigt"
    And der Kalender zeigt "Kunde bestätigt"
    When er denselben POST erneut sendet
    Then bleibt der Status "Bestätigt"
    And es wird keine doppelte fachliche Nebenwirkung erzeugt

  Scenario: Kunde lehnt ab, Termin bleibt reserviert
    Given der Kunde hat einen gültigen aktiven Bestätigungslink
    When er "Termin ablehnen" absendet
    Then ist der Bestätigungsstatus "Abgelehnt"
    And der Termin bleibt fixiert und ressourcenreserviert
    And der Administrator sieht einen roten Handlungsbedarf
    And der Termin wird nicht automatisch storniert oder neu geplant

  Scenario: Kunde wünscht Rückruf
    Given der Kunde hat einen gültigen aktiven Bestätigungslink
    When er "Rückruf wünschen" absendet
    Then ist der Status "Rückruf gewünscht"
    And der Administrator sieht die notwendige Aktion
    And der Kunde benötigt weder Konto noch App

  Scenario: Verschieben eines fixierten Termins widerruft alten Link
    Given ein Kunde hat den Termin bestätigt
    When der Administrator den fixierten Termin mit gültiger Version verschiebt
    Then wird die alte Confirmation Request widerrufen
    And der Status wird wieder "Antwort offen"
    And eine neue Tokenversion und Nachricht werden atomar geplant
    When der Kunde den alten Link öffnet
    Then sieht er eine generische Meldung, dass der Link nicht mehr gültig ist
    And der alte Link kann keinen Status ändern

  Scenario: Ungültige Tokens sind kein Existenzorakel
    When ein anonymer Benutzer zufällige, abgelaufene und widerrufene Tokens aufruft
    Then enthalten die Antworten keine Kundendaten
    And Statuscodes und Texte verraten nicht zuverlässig, ob ein Termin existiert
    And Rate Limits bremsen massenhafte Versuche

  Scenario: Confirmationseite leakt Token nicht an Dritte
    Given die öffentliche Confirmationseite ist geladen
    Then werden keine Drittanbieterassets oder Analytics geladen
    And "Referrer-Policy" ist "no-referrer"
    And die Antwort ist nicht öffentlich cachebar
    And der vollständige Link erscheint nicht in Anwendungs- oder Auditlogs

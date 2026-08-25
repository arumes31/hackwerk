@ics @calendar-feed
Feature: Standardkonformer read-only Kalenderexport
  Benutzer können Termine exportieren oder über einen privaten Feed abonnieren.

  Background:
    Given ein aktiver interner Benutzer ist angemeldet
    And ein fixierter Termin mit Kunde, Adresse, Start und Ende existiert

  Scenario: Authentifizierter ICS-Export ist parsebar
    When der Benutzer einen begrenzten Zeitraum als ICS exportiert
    Then ist der Content-Type "text/calendar; charset=utf-8"
    And die Datei enthält einen VCALENDAR und ein VEVENT
    And UID, DTSTART, DTEND, DTSTAMP und SUMMARY sind vorhanden
    And eine unabhängige ICS-Parserprüfung ist erfolgreich
    And Telefonnummer, E-Mail, Confirmationtoken und interne Secrets fehlen

  Scenario: Privater Feed wird mit einmal sichtbarem Token erzeugt
    When der Benutzer einen Feed "HackWerk alle Termine" erstellt
    Then wird eine unerratbare Feed-URL genau einmal vollständig angezeigt
    And in der Datenbank liegt nur der Tokenhash
    And die spätere Feedliste zeigt den Token nur maskiert
    And die UI warnt, dass der Link Leserechte gewährt

  Scenario: Feed funktioniert ohne Sessioncookie
    Given ein aktiver privater Feed existiert
    When ein Kalenderclient die Feed-URL ohne Login und ohne Cookie abruft
    Then erhält er den gefilterten ICS-Kalender
    And die Antwort setzt kein Sessioncookie
    And der Fahrerstandardfeed enthält alle Termine, nicht nur eigene

  Scenario: Terminverschiebung behält UID und erhöht Sequence
    Given ein Feed wurde vor einer Terminverschiebung abgerufen
    When der Administrator denselben Termin verschiebt
    And der Feed erneut abgerufen wird
    Then ist die VEVENT-UID unverändert
    And SEQUENCE ist größer
    And DTSTART und DTEND enthalten die neue Zeit

  Scenario: Abgesagter Termin wird konsistent veröffentlicht
    Given ein zuvor veröffentlichter Termin wird abgesagt
    When der Feed innerhalb des Historienfensters abgerufen wird
    Then erscheint das Event mit derselben UID
    And STATUS ist "CANCELLED"
    And SEQUENCE wurde erhöht

  Scenario: Widerruf und Benutzerdeaktivierung beenden Zugriff
    Given ein aktiver privater Feed existiert
    When der Benutzer den Feed widerruft
    Then liefert die URL keinen Kalenderinhalt mehr
    When ein neuer Feed erzeugt und danach der Benutzer deaktiviert wird
    Then liefert auch der neue Feed keinen Kalenderinhalt mehr

  Scenario: Feedtoken wird nicht geloggt
    Given ein Canary-Feedtoken wird abgerufen
    When technische App- und Accesslogs untersucht werden
    Then erscheint der vollständige Token in keinem Log
    And Metrics enthalten ihn ebenfalls nicht

  Scenario: Sonderzeichen werden korrekt escaped
    Given Kunde, Ort und Beschreibung enthalten Umlaute, Komma, Semikolon und Zeilenumbruch
    When der ICS-Feed erzeugt wird
    Then ist die Datei RFC-konform escaped und gefaltet
    And der unabhängige Parser liefert die ursprünglichen sichtbaren Werte

  Scenario: V1 ist eindeutig read-only
    When der Benutzer die Feed-Einstellungen öffnet
    Then erklärt die UI, dass Änderungen in Apple Kalender, Outlook oder Google Kalender nicht zu HackWerk zurückgeschrieben werden
    And es existiert kein Schreib-/Importendpoint für externe Kalenderänderungen

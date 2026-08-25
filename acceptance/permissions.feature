@rbac @security
Feature: Rollen und serverseitige Berechtigungen
  Die Oberfläche und jeder Application Use Case müssen Administratoren und Fahrer korrekt unterscheiden.

  Background:
    Given es existieren ein aktiver Administrator und ein aktiver Fahrer
    And beide besitzen eine gültige Session
    And ein Kunde, Auftrag, Wartelisteneintrag und Terminvorschlag existieren

  Scenario Outline: Fahrer darf operative Erfassungsfunktionen ausführen
    Given der Fahrer ist angemeldet
    When er die Aktion "<action>" ausführt
    Then ist die Aktion erlaubt

    Examples:
      | action                              |
      | Kunden anlegen                      |
      | Kunden bearbeiten                   |
      | Auftrag anlegen                     |
      | Auftrag in Warteliste aufnehmen     |
      | interne Auftragsbemerkung ergänzen  |
      | eigene Verfügbarkeit eintragen      |
      | alle geplanten Termine ansehen      |

  Scenario Outline: Fahrer darf Planungs- und Adminfunktionen nicht ausführen
    Given der Fahrer ist angemeldet
    When er einen direkten zustandsändernden Request für "<action>" sendet
    Then antwortet der Server mit "403 Forbidden"
    And der fachliche Zustand bleibt unverändert
    And es wird keine Nachricht oder Outboxnebenwirkung erzeugt

    Examples:
      | action                          |
      | Termin verschieben              |
      | Termindauer ändern              |
      | Termin fixieren                 |
      | Termin absagen                  |
      | Benutzerrolle ändern            |
      | fremde Verfügbarkeit bearbeiten |
      | Ressourcen deaktivieren         |
      | Wartelistenpriorität ändern     |

  Scenario: Alle Fahrer sehen alle Termine
    Given Fahrer A und Fahrer B existieren
    And ein Termin ist ausschließlich Fahrer A zugewiesen
    When Fahrer B den Kalender für diesen Zeitraum öffnet
    Then sieht Fahrer B den Termin
    And Fahrer B kann den Termin nicht verschieben, fixieren oder absagen

  Scenario: Fahrer kann fremden Voice-Entwurf nicht lesen oder committen
    Given Fahrer A besitzt einen Voice-Entwurf
    When Fahrer B die Entwurfs-ID direkt abruft oder committet
    Then antwortet der Server mit "404 Not Found" oder "403 Forbidden" gemäß einheitlicher Policy
    And es wird kein Kunde oder Auftrag erzeugt

  Scenario: Deaktivierter Benutzer verliert sofort Zugriff
    Given ein Fahrer besitzt eine aktive Session und einen privaten Kalenderfeed
    When der Administrator den Benutzer deaktiviert
    Then ist die bestehende Session widerrufen
    And der nächste interne Request fordert eine Anmeldung
    And der private Kalenderfeed ist nicht mehr abrufbar

  Scenario: Letzter aktiver Administrator ist geschützt
    Given genau ein aktiver Administrator existiert
    When dieser Administrator sich selbst deaktivieren oder zum Fahrer herabstufen möchte
    Then wird die Aktion mit einer verständlichen Validierung abgewiesen
    And mindestens ein aktiver Administrator bleibt bestehen

  Scenario: Fehlender CSRF-Token verändert keine Daten
    Given ein angemeldeter Benutzer besitzt ein Sessioncookie
    When ein Cross-Site-Request ohne gültigen CSRF-Nachweis eine erlaubte Mutation versucht
    Then wird der Request abgewiesen
    And der Zustand bleibt unverändert

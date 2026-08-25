@voice @privacy
Feature: Sprache erzeugt einen kontrollierten Erfassungsentwurf
  Fahrer können Daten diktieren, müssen sie aber vor dem Speichern prüfen.

  Background:
    Given die Spracheingabe ist aktiviert
    And ein Fahrer ist angemeldet
    And der Fake-Transcriber ist für Tests konfiguriert

  Scenario: Beispieldiktat wird strukturiert und geprüft
    Given die Aufnahme enthält den Satz "Franz Huber, Unterneukirchen 15, Telefonnummer 0664 1234567, ungefähr 80 Kubikmeter Holz, ungefähr drei Stunden Hackzeit, möglichst Anfang September"
    When der Fahrer die Aufnahme stoppt und zur Verarbeitung sendet
    Then entsteht ein Voice-Entwurf mit Transkript
    And Name ist "Franz Huber"
    And Adresse enthält "Unterneukirchen 15"
    And Telefon enthält "0664 1234567"
    And Holzmenge ist 80 m³
    And Hackdauer ist 180 Minuten
    And der Wunsch "Anfang September" ist sichtbar
    And unsichere Datumsableitungen sind markiert
    And es existiert noch kein Kunde, Auftrag, Wartelisteneintrag oder Termin aus diesem Entwurf

  Scenario: Expliziter Review-Commit erzeugt Kundenworkflow atomar
    Given ein Voice-Entwurf befindet sich in "Prüfung erforderlich"
    And alle Pflichtfelder wurden kontrolliert und korrigiert
    When der Fahrer "Daten geprüft und in Warteliste übernehmen" bestätigt
    Then werden Kunde, Auftrag und Wartelisteneintrag in einer Transaktion erzeugt
    And der Draft ist als committed markiert
    And kein Termin wird erzeugt oder fixiert

  Scenario: Niedrige Confidence wird nicht halluziniert
    Given der Transcriber erkennt keine eindeutige Telefonnummer und keine Menge
    When der Parser den Entwurf erzeugt
    Then bleiben Telefonnummer und Menge leer oder als "prüfen" markiert
    And es werden keine erfundenen Werte gespeichert
    And der Commit ist bis zur Pflichtfeldkorrektur blockiert

  Scenario: Mögliche Dublette wird bewusst aufgelöst
    Given ein bestehender Kunde besitzt dieselbe normalisierte Telefonnummer
    And der Voice-Entwurf erkennt diese Telefonnummer
    When die Reviewseite geladen wird
    Then zeigt sie eine Dublettenwarnung mit datensparsamem bestehenden Kundentreffer
    And der Fahrer kann bewusst den bestehenden Kunden wählen oder einen neuen anlegen
    And es erfolgt keine automatische Zusammenführung

  Scenario: Uploadlimits und falsche Dateien werden abgewiesen
    When der Fahrer eine zu große, leere oder nicht unterstützte Datei hochlädt
    Then wird der Upload vor externer Transkription abgewiesen
    And es entsteht keine unkontrollierte temporäre Datei
    And die Meldung erklärt eine manuelle Erfassungsalternative

  Scenario: Provider-Ausfall hat einen sicheren Fallback
    Given der Speech-to-Text-Provider liefert Timeout
    When die Verarbeitung läuft
    Then wird der Draft als fehlgeschlagen oder erneut versuchbar markiert
    And Audio, Transkript, API-Key und Providerpayload erscheinen nicht im technischen Log
    And der Fahrer kann die Daten manuell erfassen oder bewusst erneut versuchen

  Scenario: Audio wird kurzzeitig und nicht öffentlich gespeichert
    Given eine Aufnahme wurde hochgeladen
    Then liegt sie nur im restriktiven temporären Speicher
    And es existiert keine öffentliche Downloadroute
    When Transkription und Retentionzeit abgeschlossen sind
    Then ist das Audio gelöscht
    And das Audio ist nicht Bestandteil eines Datenbankbackups

  Scenario: Direkter Commit kann Prüfung nicht umgehen
    Given ein Draft enthält fehlende Pflichtfelder und ist nicht als geprüft markiert
    When ein Benutzer einen manipulierten Commit-Request sendet
    Then weist der Server die Mutation ab
    And es wird kein Kunde, Auftrag, Wartelisteneintrag oder Termin erzeugt

  Scenario: Spracheingabe ist optional
    Given der Browser unterstützt MediaRecorder nicht oder das Feature ist administrativ deaktiviert
    When der Fahrer die Kundenerfassung öffnet
    Then bleibt das manuelle Formular vollständig nutzbar
    And die UI behauptet nicht, dass ein Mikrofon erforderlich ist

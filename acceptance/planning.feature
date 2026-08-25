@planning @routing
Feature: Erklärbare und nicht autonome Planungsvorschläge
  Die Anwendung unterstützt den Administrator mit validen Top-3-Slots und geografischen Hinweisen.

  Background:
    Given ein Administrator ist angemeldet
    And ein Wartelistenauftrag "Huber" benötigt 180 Minuten Hackzeit
    And ein Wunschzeitraum, Fahrer-Verfügbarkeit, Ressourcen und bestehende Termine sind vorhanden

  Scenario: Engine erzeugt nachvollziehbare Top-3
    When der Administrator Planungsvorschläge für "Huber" berechnet
    Then werden höchstens drei valide Vorschläge zurückgegeben
    And jeder Vorschlag enthält Start, Ende, Fahrer, Ressourcen, Score und Gründe
    And der höchste Vorschlag ist als beste Option, nicht als Garantie, gekennzeichnet
    And kein Termin wurde dadurch angelegt oder fixiert

  Scenario: Harte Ressourcenkollision wird nie vorgeschlagen
    Given "Hackmaschine 1" ist Dienstag von 08:00 bis 11:00 reserviert
    When Vorschläge für einen weiteren dreistündigen Auftrag berechnet werden
    Then enthält kein Vorschlag einen überlappenden Slot mit "Hackmaschine 1"
    And dieser Slot geografisch und im Wunschzeitraum optimal wäre

  Scenario: Wunschzeitraum und Dringlichkeit wirken auf Rangfolge
    Given ein Auftrag wünscht die erste Septemberwoche und ist dringend
    And valide Slots innerhalb und außerhalb dieses Zeitraums existieren
    When die Vorschläge berechnet werden
    Then werden valide Slots im Wunschzeitraum gegenüber späteren gleichwertigen Slots bevorzugt
    And die Gründe nennen Wunschzeitraum und Dringlichkeit

  Scenario: Fahrstrecke berücksichtigt benachbarte Termine
    Given ein vorhandener Termin endet nahe beim Kunden "Huber"
    And ein zweiter gleichermaßen valider Slot liegt zwischen geografisch entfernten Kunden
    When der Routingadapter Entfernungen liefert
    Then erhält der nahe Slot einen besseren Routenanteil
    And die UI zeigt Distanz/Fahrzeit und Quelle
    And der Provider erhält nur notwendige Koordinaten, keine Namen oder Telefonnummern

  Scenario: Haversine-Fallback bleibt transparent
    Given der externe Routingprovider ist nicht erreichbar
    And Koordinaten sind vorhanden
    When Vorschläge berechnet werden
    Then nutzt die Engine den Haversine-Fallback
    And die UI kennzeichnet die Distanz als Luftlinie/Schätzung
    And die Vorschläge bleiben konfliktgeprüft

  Scenario: Fehlende Koordinaten erzeugen Warnung statt erfundener Distanz
    Given der Kunde besitzt keine validen Koordinaten
    When Vorschläge berechnet werden
    Then wird keine exakte Straßenentfernung behauptet
    And der Routenanteil wird gemäß dokumentierter Policy reduziert oder neutral behandelt
    And ein sonst valider Slot kann mit Warnung erscheinen

  Scenario: Übernehmen revalidiert und erzeugt nur Proposal
    Given ein Vorschlag wurde angezeigt
    And ein anderer Admin belegt danach denselben Slot
    When der erste Admin "Vorschlag übernehmen" auswählt
    Then wird der aktuelle Zustand erneut validiert
    And die Übernahme endet mit "409 Conflict" oder neuem Vorschlag
    And kein inkonsistenter Termin wird erzeugt
    And niemals wird automatisch fixiert oder benachrichtigt

  Scenario: Geografischer Gruppenhinweis
    Given mindestens drei Wartelistenaufträge liegen innerhalb der konfigurierten Clusterdistanz
    When der Administrator die Planungshinweise öffnet
    Then sieht er einen Hinweis auf die geografische Nähe
    And er kann die betroffenen Aufträge gefiltert öffnen
    And die Anwendung plant oder fixiert sie nicht automatisch

  Scenario: Identischer Input liefert stabile Reihenfolge
    Given Datenstand, Uhr, Providerantwort und Konfiguration sind identisch
    When die Vorschläge zweimal berechnet werden
    Then stimmen Kandidaten, Rangfolge und Tie-Breaker überein

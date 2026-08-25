# Task 09 – Kontrollierte Spracheingabe für Kunden- und Auftragserfassung

**Empfohlener Aufruf**

```text
$hackplan-implement Implementiere codex/tasks/09-voice-intake.md vollständig.
```

## Ziel

Ein Fahrer oder Administrator kann auf Smartphone/PC eine kurze Sprachaufnahme erstellen. Die Anwendung transkribiert und strukturiert daraus einen **Entwurf** für Kunde, Auftrag und Wartelisteneintrag. Alle erkannten Felder und Unsicherheiten werden vor dem Speichern angezeigt und müssen bewusst geprüft/bestätigt werden. Sprache fixiert niemals einen Termin.

## Vor der Implementierung lesen

- `AGENTS.md`, `PLANS.md`
- `docs/03-domain-model.md`
- `docs/05-rbac.md`
- `docs/06-ux-and-responsive.md`
- `docs/07-api-and-integrations.md`
- `docs/09-voice-intake.md`
- `docs/10-security-privacy.md`
- `docs/14-configuration.md`
- `acceptance/voice.feature`

Erstelle `docs/exec-plans/09-voice-intake.md`. Behandle Datenschutz, Browserkompatibilität und Fehlersicherheit als Hauptrisiken.

## Scope

### Domain und temporäre Entwürfe

Implementiere ein `voice_drafts`-Modell oder einen gleichwertigen serverseitigen Workflow mit:

- Besitzer/Ersteller und Ablaufzeit;
- Status `recorded|transcribing|needs_review|failed|committed|expired`;
- Transkript, erkannte strukturierte Felder, Confidence/Warnings und Provider-/Parser-Version;
- Referenz auf committed Customer/Job nach erfolgreichem Abschluss;
- niemals automatisch `appointment` oder `fixed`;
- Audio standardmäßig nur temporär außerhalb dauerhafter DB; nach Transkription/Fehler zeitnah löschen.

Definiere Aufbewahrung konfigurierbar, mit sicheren kurzen Defaults. Cleanup durch Worker/Job. Transkripte können PII enthalten und werden wie Kundendaten geschützt; keine Volltexte in Logs/Audit.

### Browseraufnahme

- Seite/Aktion „Neuen Kunden per Sprache aufnehmen“ für Admin/Fahrer.
- `MediaRecorder` mit Feature Detection, klarer Berechtigungsanfrage, Start/Pause/Stopp, Laufzeit und Abbrechen.
- Standardlimit z. B. 90 Sekunden und 15 MiB, serverseitig zwingend; Werte konfigurierbar.
- Unterstützte MIME-Typen browserabhängig aushandeln; der Server validiert tatsächlichen Content-Type/Container soweit praktikabel und vertraut nicht nur Dateiendung.
- Upload mit CSRF, Auth, Request Body Limit, Timeout und Fortschritts-/Fehlerzustand.
- Keine Aufnahme im Hintergrund nach Navigation; sichtbarer Aufnahmeindikator.
- Fallback: normales Kunden-/Auftragsformular und optional Upload einer vorhandenen Audiodatei nur, wenn gemäß Scope sinnvoll; Mikrofon ist nie Pflicht.

### Speech-to-Text Port

Definiere:

```text
Transcribe(ctx, audio, languageHint, metadata) -> transcript + segments/confidence + providerInfo
```

Adapter:

1. optionaler OpenAI-Speech-to-Text-Adapter über HTTPS, API-Key nur serverseitig, Modellname konfigurierbar;
2. Fake/Fixture-Adapter für Entwicklung und Tests;
3. deaktivierter Adapter mit klarer UI-Nachricht.

- `de`/österreichisches Deutsch als Hinweis, aber Namen/Orte nicht erzwingen.
- Providerrequest mit Timeout, Größenlimit und abbruchfähigem Context.
- API-Key/Audio/Transkript nie loggen.
- Fehler werden redigiert, Retry erfolgt bewusst und begrenzt; keine Endlosschleife.
- Dokumentiere, dass bei externem Provider Audio die eigene Infrastruktur verlässt, und mache Funktion administrativ deaktivierbar.

### Strukturierung/Extraction

Erzeuge aus Transkript Felder:

- Vor-/Nachname bzw. Firma;
- freie/strukturierte Adresse;
- Telefonnummer;
- E-Mail falls gesprochen;
- Holzmenge m³;
- Hackdauer;
- Transportwunsch/-dauer/-fahrten;
- gewünschter Zeitraum/Freitext;
- Dringlichkeit;
- Bemerkung.

Architektur:

- deterministischer Rule-based Parser für Telefonnummern, m³, Dauer, relative Monats-/Zeitausdrücke und Schlüsselwörter;
- optionaler strukturierter Extractor-Provider hinter separatem Interface, strikt gegen JSON-Schema validiert und niemals alleinige Wahrheit;
- Felder enthalten Wert, Quelle/Textspanne, Confidence und Warnungen;
- relative Angaben werden gegen Aufnahmezeit + `Europe/Vienna` interpretiert, aber vor Speichern sichtbar gemacht;
- unbekannte/mehrdeutige Daten bleiben leer/„prüfen“, nicht halluziniert;
- keine automatischen Google-Maps-/Geocoder-Entscheidungen.

### Review- und Commit-UI

Nach Verarbeitung:

- zeige Transkript und ein normales editierbares Kunden-/Auftragsformular nebeneinander bzw. mobil untereinander;
- markiere niedrige Confidence, fehlende Pflichtfelder und widersprüchliche Angaben;
- Nutzer kann Aufnahme/Entwurf verwerfen, neu transkribieren oder Werte korrigieren;
- Checkbox/Bestätigungsaktion „Daten geprüft“ ist vor Commit erforderlich, aber nicht als bloße UI-Sicherheit: Server akzeptiert nur expliziten Commit-Use-Case;
- Commit legt Customer + Job + Waitlist Entry atomar an, nutzt dieselben Domainvalidierungen und Dublettenwarnungen wie Task 02;
- bei möglicher Dublette bietet UI „bestehenden Kunden verwenden“ nach bewusster Auswahl, niemals automatische Fusion;
- Audit `voice.draft_committed` speichert Draft-ID und erstellte IDs, nicht Audio/Transkript.

### Beispiel

Der Satz

> „Franz Huber, Unterneukirchen 15, Telefonnummer 0664 1234567, ungefähr 80 Kubikmeter Holz, ungefähr drei Stunden Hackzeit, möglichst Anfang September.“

soll mindestens einen prüfbaren Entwurf ergeben:

- Name: Franz Huber
- Adresse/Freitext: Unterneukirchen 15
- Telefon: 0664 1234567
- Holz: 80 m³
- Hackdauer: 180 Minuten
- Wunsch: Anfang September, strukturiertes Datum nur falls eindeutig aus aktuellem Jahr ableitbar und sichtbar markiert

### Security/Privacy

- Audioverzeichnis nicht weböffentlich, zufällige Dateinamen, restriktive Rechte, Quota und Cleanup.
- Container erhält beschreibbares `tmpfs`/dediziertes temporäres Volume trotz read-only root filesystem.
- Dateidekompression/Transcoding nur wenn notwendig und in begrenztem subprocess/container; keine Shellinterpolation.
- Rate Limits pro Nutzer, gleichzeitige Aufnahmejobs begrenzen.
- Keine Audio-/Transkriptinhalte in Sentry/Telemetry/Logs.
- Admin kann Funktion/Provider deaktivieren und Retention sehen.
- Datenschutzhinweis vor erster Aufnahme und im Einstellungsbereich.

### Tests

- Parser-Fixtures für österreichische Namen/Orte/Telefonnummern, m³, Stunden/Minuten, Transport und Monatsangaben.
- Tests für fehlende/mehrdeutige Felder und keine Halluzination.
- Uploadsecurity: zu groß, falscher Typ, leere Datei, abgebrochener Upload, Providerzeitüberschreitung.
- E2E mit Fakeadapter: aufnehmen oder Fixture hochladen, Review, korrigieren, committen; Termin bleibt unberührt.
- Cleanup-/Retentiontests und keine PII in Logs.
- Rechte: Fahrer darf erfassen, anderer Nutzer darf Draft nicht lesen/committen, Adminzugriff nur gemäß dokumentierter Supportpolicy.

## Verbindliche Regeln

- Kein ungeprüftes Auto-Save.
- Kein Termin und keine Fixierung aus Sprachpfad.
- Dieselben Validierungen/RBAC wie manuelle Erfassung.
- Provider optional und deaktivierbar; Anwendung bleibt ohne ihn nutzbar.
- Roh-Audio wird nicht dauerhaft aufbewahrt.
- Keine erfundenen Felder bei Unsicherheit.

## Nicht Bestandteil

- Dauerlauschen/Hotword;
- Sprachsteuerung während der Fahrt als Ersatz für sichere Fahrzeugbedienung;
- vollautomatisches Geocoding oder Terminplanung;
- native mobile App;
- Speicherung von Audio als Kundenaktenanhang.

## Akzeptanzkriterien

- [ ] Unterstützter Browser kann Aufnahme starten/stoppen und einen sicheren Upload senden.
- [ ] Fake- und optionaler OpenAI-Transcriber sind hinter demselben Port implementiert.
- [ ] Beispieltext wird in einen sinnvollen, editierbaren Entwurf strukturiert.
- [ ] Confidence/Warnungen und fehlende Pflichtfelder sind sichtbar.
- [ ] Nutzer muss prüfen und explizit committen.
- [ ] Commit erzeugt Kunde/Auftrag/Warteliste atomar und nutzt Dubletten-/Validierungslogik.
- [ ] Kein Sprachpfad kann einen Termin anlegen oder fixieren.
- [ ] Audio wird nach konfigurierter kurzer Retention gelöscht und ist nicht öffentlich abrufbar.
- [ ] Providerfehler und nicht unterstützte Browser haben verständliche Fallbacks.
- [ ] `acceptance/voice.feature` ist automatisiert.

## Pflichtprüfungen

```bash
make generate
make format
make lint
make test
make test-integration
make test-e2e
make test-race
make build
make check
```

Prüfe zusätzlich Uploadlimits, temporäres Dateisystem, Cleanup, Logredaction und einen direkten Request, der Review/Commit zu umgehen versucht.

## Abschlussbericht

Beschreibe Browser-/Provider-Fallbacks, Parserstrategie, Confidence, Retention, Datenfluss des Audios und Securitytests. Weise explizit nach, dass weder Auto-Save noch Terminfixierung möglich ist.

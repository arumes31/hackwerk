# Spracheingabe und kontrollierte Auftragserfassung

## Ziel

Ein Fahrer kann auf dem Smartphone einen gesprochenen Kundenkontakt in einen editierbaren Entwurf verwandeln. Die Funktion reduziert Tipparbeit, ersetzt aber weder Validierung noch menschliche Entscheidung.

## Ablauf

```text
Aufnahme starten
  -> Audio lokal erfassen
  -> Aufnahme stoppen
  -> Größen-/Formatprüfung
  -> Transkription
  -> strukturierte Extraktion
  -> Entwurfsformular mit Konfidenz
  -> Nutzer korrigiert
  -> expliziter Commit
  -> Kunde + Auftrag + Warteliste atomar
```

## Browseraufnahme

- `MediaRecorder` progressive enhancement;
- erlaubte Formate nach Browsererkennung, bevorzugt WebM/Opus oder MP4/AAC;
- maximale Dauer Default 90 Sekunden;
- maximale Uploadgröße Default 15 MiB;
- sichtbarer Timer und Stop-Button;
- Aufnahme wird nicht im Hintergrund fortgesetzt;
- bei fehlender Unterstützung normale Formularerfassung anbieten;
- kein automatisches Senden direkt nach Mikrofonfreigabe.

## Providerarchitektur

### Transcriber

- `DisabledTranscriber`: Feature verständlich deaktiviert;
- `FakeTranscriber`: deterministische Tests;
- `OpenAITranscriber`: optional, serverseitig, Modell per Konfiguration, Sprache `de`/Kontext `de-AT`;
- später lokaler Whisper-kompatibler Adapter möglich.

### Extractor

- `RuleBasedExtractor`: Telefon, m³, Dauerangaben, Monatsnamen, Dringlichkeitsphrasen;
- optional `StructuredExtractor` über konfigurierten Modellprovider;
- beide liefern dasselbe `VoiceDraft`-Schema.

## Entwurfsschema

```json
{
  "transcript": "Franz Huber, Unterneukirchen 15, ...",
  "fields": {
    "first_name": {"value": "Franz", "confidence": 0.94, "source": "Franz Huber"},
    "last_name": {"value": "Huber", "confidence": 0.94, "source": "Franz Huber"},
    "address_freeform": {"value": "Unterneukirchen 15", "confidence": 0.73, "source": "..."},
    "phone_raw": {"value": "0664 1234567", "confidence": 0.91, "source": "..."},
    "volume_m3": {"value": 80, "confidence": 0.98, "source": "80 Kubikmeter"},
    "estimated_hack_minutes": {"value": 180, "confidence": 0.97, "source": "drei Stunden"},
    "preference_text": {"value": "Anfang September", "confidence": 0.88, "source": "..."}
  },
  "warnings": ["Postleitzahl fehlt", "Auftragstyp nicht erkannt"]
}
```

Das Schema unterscheidet `null` von erfundenen Defaultwerten. Unsichere Felder werden nicht still ergänzt.

## Extraktionsregeln

- österreichische und internationale Telefonnummern akzeptieren, Raw-Wert bewahren;
- `Kubikmeter`, `m3`, `m³` erkennen;
- Stunden/Minuten in ganze Minuten umrechnen;
- „Anfang/Mitte/Ende <Monat>“ als Text bewahren und nur dann in Datumsbereich übersetzen, wenn das Jahr eindeutig ist;
- keine Orts-, PLZ- oder Firmeninformationen halluzinieren;
- „mit Transport“/„ohne Transport“ erkennen;
- „möglichst bald“, „dringend“ usw. in Dringlichkeit plus Originaltext abbilden;
- Dialekt oder unsichere Wörter im Transkript unverändert sichtbar lassen.

## Review-UI

- Transkript und Felder nebeneinander/untereinander;
- Konfidenz < 0,75 sichtbar markieren;
- Pflichtfelder mit verständlichen Fehlern;
- Adressfeld darf vorerst freeform bleiben;
- Auftragstyp muss explizit bestätigt werden;
- Buttons: „Neu aufnehmen“, „Entwurf löschen“, „Speichern und auf Warteliste setzen“;
- nach Commit Links zur Kundenakte und zum Auftrag;
- Commit ist idempotent.

## Datenschutz und Speicherung

- Audio standardmäßig nur temporär in begrenztem `tmpfs` und unmittelbar nach Providerantwort löschen;
- keine Audioinhalte in Logs;
- Transkript/Entwurf mit konfigurierbarer kurzen Aufbewahrung, Default 24 Stunden bei nicht committed;
- klarer Admin-Schalter für externen Sprachprovider;
- UI-Hinweis, wenn Daten an externen Provider übertragen werden;
- API-Key ausschließlich serverseitig und aus Secret;
- technische Fehler dürfen kein vollständiges Transkript spiegeln.

## Sicherheit

- Authentifizierung und Rolle erforderlich;
- Upload-Rate-Limit pro Benutzer;
- MIME-Sniffing und erlaubte Formate;
- Body-/Zeitlimit;
- zufällige temporäre Dateinamen, keine Nutzerpfade;
- keine Shell-Aufrufe mit Dateinamen;
- Provider-Timeout und begrenzte Antwortgröße;
- strukturierte Modellantwort strikt validieren.

## Nicht zulässig

- automatisches Fixieren eines Termins;
- automatisches Versenden einer Nachricht;
- Speichern ohne Review-POST;
- Übernahme erfundener Werte;
- unbegrenzte Audioaufbewahrung;
- API-Key im Browser.

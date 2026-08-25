# Paketvalidierung

Stand: 25. August 2026

Das Prompt-Paket wurde nach der Erstellung automatisiert geprüft.

## Umfang vor Prüfnachweisen

- 63 inhaltliche Dateien;
- 6.264 Textzeilen;
- 12 sequenzielle Implementierungsaufträge;
- 4 unabhängige Review-Aufträge;
- 3 repository-lokale Codex Skills;
- 8 Gherkin-Dateien mit 62 Szenarien;
- 216 geprüfte interne Dateiverweise.

## Erfolgreiche Prüfungen

- alle Dateien sind valides UTF-8;
- alle erforderlichen Tasks `00`–`11` und Reviews `90`–`93` liegen genau einmal vor;
- Markdown-Codeblöcke sind ausgeglichen;
- Skill-Dateien besitzen gültige `name`-/`description`-Frontmatter;
- Gherkin-Dateien verwenden eine konsistente Grundsyntax;
- CSV-Referenztabellen besitzen konsistente Spaltenzahlen;
- alle konkreten internen Dokumentverweise zeigen auf vorhandene Dateien;
- keine privaten Schlüssel oder typischen API-Tokenmuster gefunden;
- keine Symlinks oder unerwarteten Binärdateien enthalten.

`MANIFEST.sha256` enthält SHA-256-Prüfsummen für alle Paketdateien außer der Prüfsummendatei selbst.

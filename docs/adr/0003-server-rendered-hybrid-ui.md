# ADR 0003: Serverseitig gerenderte Hybrid-UI

## Status
Angenommen.

## Entscheidung
`templ` + `htmx 2.x` für Seiten/Formulare, FullCalendar + kleine browsernative JavaScript-Module für Kalender/Audio. Browserbibliotheken werden als geprüfte, fest versionierte Distributionsdateien direkt in das Go-Binary eingebettet; Node und npm sind weder Build- noch Laufzeitabhängigkeiten.

## Begründung
Weniger SPA-Komplexität, einfache Auth/CSRF, dennoch hochwertige Kalenderinteraktion und mobile Formulare.

## Folgen
HTML- und JSON-Flows teilen Application Services. Keine Browser-Global-State-Library. Assets lokal und ohne Frontend-Buildschritt einbetten.

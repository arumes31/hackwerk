# MapLibre GL JS

HackWerk bundles the Content Security Policy compatible distribution of
MapLibre GL JS 5.19.0. The files are served locally by the Go web service;
there is no Node/npm build step and no runtime CDN dependency.

Upstream package files:

- `https://unpkg.com/maplibre-gl@5.19.0/dist/maplibre-gl-csp.js`
- `https://unpkg.com/maplibre-gl@5.19.0/dist/maplibre-gl-csp-worker.js`
- `https://unpkg.com/maplibre-gl@5.19.0/dist/maplibre-gl.css`

License: BSD-3-Clause, see
`https://github.com/maplibre/maplibre-gl-js/blob/v5.19.0/LICENSE.txt`.

## SHA-256

| File | SHA-256 |
| --- | --- |
| `maplibre-gl-csp.js` | `a6be9ebbd932bc8e0733147eda04819becdcc453829316a0b1bad1b4e5619460` |
| `maplibre-gl-csp-worker.js` | `b83ff8093df2258e89f9ea6e4e3e877066510cd0688049efac9aa47928595a85` |
| `maplibre-gl.css` | `761a1130f0960ea369d917ec843f7d1af5ca1dcf27701aef1d9d59821b3bab25` |

The browser builds a raster style locally and requests tiles only through the
same-origin template `/map/tiles/{z}/{x}/{y}`. Provider URLs and credentials
remain server-side. The configured attribution is passed as text and escaped
before MapLibre renders it.

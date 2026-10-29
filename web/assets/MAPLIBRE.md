# MapLibre GL JS

HackWerk bundles the Content Security Policy compatible distribution of
MapLibre GL JS 5.24.0 with a small set of local security patches (see below).
The files are served locally by the Go web service; there is no Node/npm build
step and no runtime CDN dependency.

Upstream package: `https://registry.npmjs.org/maplibre-gl/-/maplibre-gl-5.24.0.tgz`
(npm `dist.integrity`
`sha512-ALyFxgtd5R+65UqZ/++lOqwWcC0SNho9c27fYSyLmG7AfnAul2o46F05aDJGPbFU57wos9dgcIySHs0Xe6ia3A==`,
`dist.shasum` `a8059371cdbeb04a62850ccc22cb37783928a10d`). Vendored files from
`package/dist/`:

- `maplibre-gl-csp.js`
- `maplibre-gl-csp-worker.js`
- `maplibre-gl.css`

License: BSD-3-Clause, see
`https://github.com/maplibre/maplibre-gl-js/blob/v5.24.0/LICENSE.txt`.

## SHA-256

Upstream (unpatched) files as shipped in the npm tarball:

| File | SHA-256 |
| --- | --- |
| `maplibre-gl-csp.js` | `a1f1847bac64aa00acbf80fbb79b2c5af24d8eecaaa5e2ad14080fab81f1de95` |
| `maplibre-gl-csp-worker.js` | `f950e7b15c49c8b9c7bb52136df2a2df2f6f03b83b8927774573fc98b067f7f0` |
| `maplibre-gl.css` | `ab1e70d59ec40465bae7e7030da2f3ccf28133fd502e62bd598eefbadfd7a732` |

Vendored files after `go run ./scripts/maplibre-patch` (LF line endings):

| File | SHA-256 |
| --- | --- |
| `maplibre-gl-csp.js` | `01c95acef6d856b6d53e5cf11bd1def854de042f276357456155f996f20cc277` |
| `maplibre-gl-csp-worker.js` | `9c96c759d8f6c3a64171df35e78f106dfd28f8d6a6d4d2421e72b1fba96e2758` |
| `maplibre-gl.css` | unchanged |

## Local security patches

`scripts/maplibre-patch` applies exact, single-match string replacements to the
minified bundles and aborts if a patch site is missing or ambiguous. It is
idempotent. To update MapLibre: copy the three pristine `dist` files from a
verified tarball, adjust the version in the patch header and the minified patch
sites in `scripts/maplibre-patch/main.go`, run it, and update the hashes above.
`web/assets/maplibre_patch_test.go` fails if a patch is missing.

None of these issues was fixed upstream as of 5.24.0.

1. Attribution sanitizer (`DOM.removeAttributes`, CWE-79). Upstream removes
   attributes while iterating the live `element.attributes` collection, so an
   `on*` attribute directly following a removed one can survive and is then
   assigned via `innerHTML` in the attribution control. Patched to iterate
   `Array.from(element.attributes)`. Main bundle only.
2. Sprite rectangles (CWE-789). Sprite JSON `x`, `y`, `width`, `height` were
   passed unchecked to `getImageData`. The sprite loader now skips entries that
   are non-finite, non-positive or outside the sprite sheet. Main bundle only.
3. MLT allocations (CWE-789). The MLT varint decoders (`Uint32Array` and
   `BigUint64Array`) now reject a value count larger than the remaining input
   bytes (every varint needs at least one byte). The FastPFOR decoder rejects a
   value count above 256 values per encoded byte (each 256-value block needs at
   least two metadata bytes) before allocating its output buffer. Both bundles.
   Not patched: RLE-decoded streams (`numRleValues`) legitimately expand
   without a byte bound, so no plausible limit exists there.
4. MVT ClosePath (CWE-400). The MVT spec requires a ClosePath command count of
   1. `loadGeometry` and `bbox` now clamp a larger count to 1 instead of
   cloning points (or spinning) without consuming input. Both bundles.

## Exposure in HackWerk

The browser builds a raster style locally and requests tiles only through the
same-origin template `/map/tiles/{z}/{x}/{y}`. Provider URLs and credentials
remain server-side. No TileJSON, sprite, glyph, vector (MVT) or MLT source is
loaded, so the patched code paths are not reachable with attacker-controlled
input today; the patches are defense in depth. The configured attribution is
set in code, passed as text and escaped before MapLibre renders it, and the
CSP restricts `connect-src` and `img-src` to `'self'` (plus `data:` images).

The CSP build's worker is configured with MapLibre's `setWorkerUrl` API and
the content-versioned, same-origin worker asset before a map is created.

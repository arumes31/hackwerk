package assets

import (
	"strings"
	"testing"
)

// TestVendoredMapLibreCarriesSecurityPatches guards the local MapLibre
// patches documented in MAPLIBRE.md and applied by scripts/maplibre-patch.
func TestVendoredMapLibreCarriesSecurityPatches(t *testing.T) {
	t.Parallel()

	for name, contracts := range map[string][]string{
		"static/maplibre-gl-csp.js": {
			"maplibre-gl-js/blob/v5.24.0/LICENSE.txt",
			"HackWerk security patches applied by scripts/maplibre-patch",
			"of Array.from(e.attributes))",
			"a+t<=n.canvas.width&&l+s<=n.canvas.height))continue;",
			"function mf(e,t,i){if(!(i<=e.length-t.get()))throw",
			"function _f(e,t,i){if(!(i<=e.length-t.get()))throw",
			"if(!(t<=1024*e.length+256))throw",
			"n=7&t,s=t>>3,7===n&&s>1&&(s=1)}",
			"i=7&t,r=t>>3,7===i&&r>1&&(r=1)}",
		},
		"static/maplibre-gl-csp-worker.js": {
			"maplibre-gl-js/blob/v5.24.0/LICENSE.txt",
			"HackWerk security patches applied by scripts/maplibre-patch",
			"function dp(t,e,r){if(!(r<=t.length-e.get()))throw",
			"function yp(t,e,r){if(!(r<=t.length-e.get()))throw",
			"if(!(e<=1024*t.length+256))throw",
			"s=7&e,o=e>>3,7===s&&o>1&&(o=1)}",
			"r=7&e,n=e>>3,7===r&&n>1&&(n=1)}",
		},
	} {
		content, err := Files.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		bundle := string(content)
		for _, contract := range contracts {
			if strings.Count(bundle, contract) != 1 {
				t.Errorf("%s: missing MapLibre security patch %q; run go run ./scripts/maplibre-patch", name, contract)
			}
		}
	}

	mainBundle, err := Files.ReadFile("static/maplibre-gl-csp.js")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(mainBundle), "of e.attributes)") {
		t.Error("MapLibre attribution sanitizer still iterates the live attributes collection")
	}
}

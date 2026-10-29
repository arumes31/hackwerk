// Command maplibre-patch applies HackWerk's defense-in-depth patches to the
// vendored MapLibre GL JS CSP bundles in web/assets/static.
//
// Usage: go run ./scripts/maplibre-patch [web/assets/static]
//
// Each patch is an exact string replacement that must match exactly once in
// the pristine upstream bundle. Already patched files are left unchanged, so
// the command is idempotent. A missing or ambiguous patch site aborts without
// writing anything; that signals an upstream change that needs a manual
// review. See web/assets/MAPLIBRE.md for the rationale of every patch.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type patch struct {
	name string
	old  string
	new  string
}

const headerOld = "@license 3-Clause BSD. Full text of license: https://github.com/maplibre/maplibre-gl-js/blob/v5.24.0/LICENSE.txt\n */\n"

const headerNew = headerOld + "/* HackWerk security patches applied by scripts/maplibre-patch; see web/assets/MAPLIBRE.md */\n"

var patches = map[string][]patch{
	"maplibre-gl-csp.js": {
		{"header", headerOld, headerNew},
		{
			"attribution sanitizer iterates an attribute snapshot",
			"static removeAttributes(e){for(const{name:t,value:i}of e.attributes)",
			"static removeAttributes(e){for(const{name:t,value:i}of Array.from(e.attributes))",
		},
		{
			"sprite rectangles must be finite, positive and inside the sheet",
			"const{width:t,height:s,x:a,y:l,sdf:c,pixelRatio:h,stretchX:u,stretchY:d,content:p,textFitWidth:f,textFitHeight:m}=o[e];",
			"const{width:t,height:s,x:a,y:l,sdf:c,pixelRatio:h,stretchX:u,stretchY:d,content:p,textFitWidth:f,textFitHeight:m}=o[e];" +
				"if(!(Number.isFinite(t)&&Number.isFinite(s)&&Number.isFinite(a)&&Number.isFinite(l)&&t>0&&s>0&&a>=0&&l>=0&&a+t<=n.canvas.width&&l+s<=n.canvas.height))continue;",
		},
		{
			"MLT varint count bounded by remaining bytes",
			"function mf(e,t,i){const r=new Uint32Array(i);",
			"function mf(e,t,i){if(!(i<=e.length-t.get()))throw new Error(\"MLT: varint count exceeds stream\");const r=new Uint32Array(i);",
		},
		{
			"MLT varint64 count bounded by remaining bytes",
			"function _f(e,t,i){const r=new BigUint64Array(i);",
			"function _f(e,t,i){if(!(i<=e.length-t.get()))throw new Error(\"MLT: varint count exceeds stream\");const r=new BigUint64Array(i);",
		},
		{
			"MLT FastPFOR value count bounded by encoded words",
			"l=function(e,t,i){let r=0,n=0;const o=new Uint32Array(t)",
			"l=function(e,t,i){if(!(t<=1024*e.length+256))throw new Error(\"FastPFOR: numValues exceeds stream\");let r=0,n=0;const o=new Uint32Array(t)",
		},
		{
			"MVT loadGeometry ClosePath count clamped to 1",
			"{const t=e.readVarint();n=7&t,s=t>>3}",
			"{const t=e.readVarint();n=7&t,s=t>>3,7===n&&s>1&&(s=1)}",
		},
		{
			"MVT bbox ClosePath count clamped to 1",
			"{const t=e.readVarint();i=7&t,r=t>>3}",
			"{const t=e.readVarint();i=7&t,r=t>>3,7===i&&r>1&&(r=1)}",
		},
	},
	"maplibre-gl-csp-worker.js": {
		{"header", headerOld, headerNew},
		{
			"MLT varint count bounded by remaining bytes",
			"function dp(t,e,r){const n=new Uint32Array(r);",
			"function dp(t,e,r){if(!(r<=t.length-e.get()))throw new Error(\"MLT: varint count exceeds stream\");const n=new Uint32Array(r);",
		},
		{
			"MLT varint64 count bounded by remaining bytes",
			"function yp(t,e,r){const n=new BigUint64Array(r);",
			"function yp(t,e,r){if(!(r<=t.length-e.get()))throw new Error(\"MLT: varint count exceeds stream\");const n=new BigUint64Array(r);",
		},
		{
			"MLT FastPFOR value count bounded by encoded words",
			"l=function(t,e,r){let n=0,i=0;const s=new Uint32Array(e)",
			"l=function(t,e,r){if(!(e<=1024*t.length+256))throw new Error(\"FastPFOR: numValues exceeds stream\");let n=0,i=0;const s=new Uint32Array(e)",
		},
		{
			"MVT loadGeometry ClosePath count clamped to 1",
			"{const e=t.readVarint();s=7&e,o=e>>3}",
			"{const e=t.readVarint();s=7&e,o=e>>3,7===s&&o>1&&(o=1)}",
		},
		{
			"MVT bbox ClosePath count clamped to 1",
			"{const e=t.readVarint();r=7&e,n=e>>3}",
			"{const e=t.readVarint();r=7&e,n=e>>3,7===r&&n>1&&(n=1)}",
		},
	},
}

func main() {
	dir := filepath.Join("web", "assets", "static")
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	for _, name := range []string{"maplibre-gl-csp.js", "maplibre-gl-csp-worker.js"} {
		if err := apply(filepath.Join(dir, name), patches[name]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}

//nolint:gosec // Paths are the fixed vendored bundle names; written assets stay world-readable.
func apply(path string, list []patch) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	content := strings.ReplaceAll(string(raw), "\r\n", "\n")
	for _, p := range list {
		if strings.Count(content, p.new) == 1 {
			fmt.Printf("%s: already applied: %s\n", filepath.Base(path), p.name)
			continue
		}
		if n := strings.Count(content, p.old); n != 1 {
			return fmt.Errorf("%s: patch site for %q found %d times, want exactly 1", path, p.name, n)
		}
		content = strings.Replace(content, p.old, p.new, 1)
		fmt.Printf("%s: applied: %s\n", filepath.Base(path), p.name)
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

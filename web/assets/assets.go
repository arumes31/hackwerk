// Package assets embeds HackWerk's directly served browser assets.
package assets

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path"
)

// Files contains local CSS and JavaScript. No Node/npm build is required.
//
//go:embed static/*
var Files embed.FS

// Paths lists the public URLs of the current asset bundle.
type Paths struct {
	JavaScript                  string
	RouteLocationsJavaScript    string
	CSS                         string
	ControlFoundationCSS        string
	Manifest                    string
	Icon                        string
	LoginOriginalCSS            string
	LoginCSS                    string
	LoginLoaderJavaScript       string
	LoginBackgroundJavaScript   string
	MapLibreJavaScript          string
	MapLibreWorker              string
	MapLibreCSS                 string
	FullCalendarJavaScript      string
	FullCalendarThemeJavaScript string
	FullCalendarSkeletonCSS     string
	FullCalendarThemeCSS        string
	FullCalendarPaletteCSS      string
}

const publicPrefix = "/assets/"

// LoadPaths returns content-versioned local asset paths. Changing an embedded
// file changes its URL without requiring a Node/npm build step.
func LoadPaths() (Paths, error) {
	versions, err := LoadVersions()
	if err != nil {
		return Paths{}, err
	}
	required := []string{
		"app.js", "route-locations.js", "app.css", "control-foundation.css", "manifest.json", "hackwerk-icon.svg",
		"login-original.css", "login.css", "login-background-loader.js", "login-background.js",
		"maplibre-gl-csp.js", "maplibre-gl-csp-worker.js", "maplibre-gl.css",
		"fullcalendar.min.js", "fullcalendar-theme.min.js", "fullcalendar-skeleton.css",
		"fullcalendar-theme.css", "fullcalendar-palette.css",
	}
	for _, name := range required {
		if len(versions[publicPrefix+name]) < 12 {
			return Paths{}, fmt.Errorf("assets: required file %q is not embedded", name)
		}
	}
	versioned := func(name string) string {
		return publicPrefix + name + "?v=" + versions[publicPrefix+name][:12]
	}

	return Paths{
		JavaScript:                  versioned("app.js"),
		RouteLocationsJavaScript:    versioned("route-locations.js"),
		CSS:                         versioned("app.css"),
		ControlFoundationCSS:        versioned("control-foundation.css"),
		Manifest:                    versioned("manifest.json"),
		Icon:                        versioned("hackwerk-icon.svg"),
		LoginOriginalCSS:            versioned("login-original.css"),
		LoginCSS:                    versioned("login.css"),
		LoginLoaderJavaScript:       versioned("login-background-loader.js"),
		LoginBackgroundJavaScript:   versioned("login-background.js"),
		MapLibreJavaScript:          versioned("maplibre-gl-csp.js"),
		MapLibreWorker:              versioned("maplibre-gl-csp-worker.js"),
		MapLibreCSS:                 versioned("maplibre-gl.css"),
		FullCalendarJavaScript:      versioned("fullcalendar.min.js"),
		FullCalendarThemeJavaScript: versioned("fullcalendar-theme.min.js"),
		FullCalendarSkeletonCSS:     versioned("fullcalendar-skeleton.css"),
		FullCalendarThemeCSS:        versioned("fullcalendar-theme.css"),
		FullCalendarPaletteCSS:      versioned("fullcalendar-palette.css"),
	}, nil
}

// LoadVersions returns the SHA-256 digest for every embedded public asset,
// keyed by its public request path.
func LoadVersions() (map[string]string, error) {
	versions := make(map[string]string)
	err := fs.WalkDir(Files, "static", func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		contents, err := Files.ReadFile(filePath)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(contents)
		name := path.Clean(filePath[len("static/"):])
		versions[publicPrefix+name] = hex.EncodeToString(digest[:])
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("assets: hashing static files: %w", err)
	}
	return versions, nil
}

// PublicFS returns the embedded static directory for http.FileServer.
func PublicFS() (fs.FS, error) {
	public, err := fs.Sub(Files, "static")
	if err != nil {
		return nil, fmt.Errorf("assets: opening static directory: %w", err)
	}
	return public, nil
}

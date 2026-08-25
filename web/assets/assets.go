// Package assets embeds HackWerk's directly served browser assets.
package assets

import (
	"embed"
	"fmt"
	"io/fs"
)

// Files contains local CSS and JavaScript. No Node/npm build is required.
//
//go:embed static/*
var Files embed.FS

// Paths lists the public URLs of the current asset bundle.
type Paths struct {
	JavaScript                  string
	CSS                         string
	FullCalendarJavaScript      string
	FullCalendarThemeJavaScript string
	FullCalendarSkeletonCSS     string
	FullCalendarThemeCSS        string
	FullCalendarPaletteCSS      string
}

// LoadPaths returns stable local asset paths.
func LoadPaths() (Paths, error) {
	return Paths{
		JavaScript: "/assets/app.js", CSS: "/assets/app.css",
		FullCalendarJavaScript:      "/assets/fullcalendar.min.js",
		FullCalendarThemeJavaScript: "/assets/fullcalendar-theme.min.js",
		FullCalendarSkeletonCSS:     "/assets/fullcalendar-skeleton.css",
		FullCalendarThemeCSS:        "/assets/fullcalendar-theme.css",
		FullCalendarPaletteCSS:      "/assets/fullcalendar-palette.css",
	}, nil
}

// PublicFS returns the embedded static directory for http.FileServer.
func PublicFS() (fs.FS, error) {
	public, err := fs.Sub(Files, "static")
	if err != nil {
		return nil, fmt.Errorf("assets: opening static directory: %w", err)
	}
	return public, nil
}

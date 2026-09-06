// Package buildinfo exposes immutable build metadata injected by the linker.
package buildinfo

import "strings"

// These values are replaced with -ldflags for release builds.
var (
	Version   = "dev"
	Release   = "dev"
	Commit    = "unknown"
	BuildTime = "unknown"
)

// Info is the safe, public representation of the running build.
type Info struct {
	Version   string `json:"version"`
	Release   string `json:"release"`
	Commit    string `json:"commit"`
	BuildTime string `json:"build_time"`
}

// Current returns the metadata compiled into the current binary.
func Current() Info {
	return Info{
		Version:   Version,
		Release:   Release,
		Commit:    Commit,
		BuildTime: BuildTime,
	}
}

// DisplayVersion returns a concise build identity for user-facing diagnostics.
func (info Info) DisplayVersion() string {
	version := strings.TrimSpace(info.Version)
	if version == "" {
		version = "dev"
	}

	commit := strings.TrimSpace(info.Commit)
	if commit == "" || commit == "unknown" {
		return version
	}
	if len(commit) > 7 {
		commit = commit[:7]
	}
	return version + " · " + commit
}

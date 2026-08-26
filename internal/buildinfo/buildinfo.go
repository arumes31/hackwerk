// Package buildinfo exposes immutable build metadata injected by the linker.
package buildinfo

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

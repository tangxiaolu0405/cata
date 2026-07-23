// Package version holds the build-time version string.
package version

// Version is set at link time via:
//
//	-ldflags "-X cata/internal/cata/version.Version=v0.1.x"
//
// Local / unset builds remain "dev".
var Version = "dev"

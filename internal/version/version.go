// Package version exposes the build version, set via ldflags by GoReleaser.
package version

// Version is overridden at build time; "dev" when built from source.
var Version = "dev"

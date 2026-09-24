// Package version carries the build version; release builds set it with -ldflags.
package version

// Version is "dev" unless set at build time:
// -ldflags "-X github.com/hiway-media/gpuledger/internal/version.Version=v0.1.0".
var Version = "dev"

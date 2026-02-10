// Package version exposes the application version derived from build metadata.
//
// Go 1.18+ automatically embeds VCS info (git commit, dirty flag, etc.)
// into the binary via runtime/debug.BuildInfo. No -ldflags required.
//
// Usage:
//
//	version.GitCommit  // "a3f8c2d1" or "dev"
//	version.Full()     // "tarsy/a3f8c2d1" or "tarsy/dev"
package version

import "runtime/debug"

// AppName is the application name used in version strings and protocol handshakes.
const AppName = "tarsy"

// GitCommit is the short git commit hash (8 chars) from build info.
// Set to "dev" when build info is unavailable (e.g., `go test`, non-git builds).
var GitCommit = initGitCommit()

func initGitCommit() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && s.Value != "" {
			if len(s.Value) > 8 {
				return s.Value[:8]
			}
			return s.Value
		}
	}
	return "dev"
}

// Full returns "tarsy/<commit>" for use in user-agent strings, logging, etc.
func Full() string {
	return AppName + "/" + GitCommit
}

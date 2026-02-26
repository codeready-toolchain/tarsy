// Package version exposes the application version derived from build metadata.
//
// Priority: -ldflags override > VCS info from debug.BuildInfo > "dev" fallback.
//
// Usage:
//
//	version.GitCommit  // "a3f8c2d1" or "dev"
//	version.Full()     // "tarsy/a3f8c2d1" or "tarsy/dev"
package version

import "runtime/debug"

// AppName is the application name used in version strings and protocol handshakes.
const AppName = "tarsy"

// gitCommitOverride is set via -ldflags at build time for container builds
// where .git is unavailable. Empty string means no override.
var gitCommitOverride string

// GitCommit is the short git commit hash (8 chars) from build info.
// Set to "dev" when build info is unavailable (e.g., `go test`, non-git builds).
var GitCommit = initGitCommit()

func initGitCommit() string {
	if gitCommitOverride != "" {
		if len(gitCommitOverride) > 8 {
			return gitCommitOverride[:8]
		}
		return gitCommitOverride
	}
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

// Package version exposes build information injected at link time.
//
// It is for reporting only. Nothing in the application may branch on it.
package version

// Version and Commit are set with -ldflags during release builds.
var (
	// Version is the semantic version or a git describe string.
	Version = "dev"
	// Commit is the short git commit hash.
	Commit = "none"
)

// String returns a single-line description suitable for --version output.
func String() string {
	return "kest " + Version + " (" + Commit + ")"
}

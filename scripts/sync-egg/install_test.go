package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeCurl answers GitHub API URLs the way the real service does for a
// repository that has only a pre-release: "latest" is a 404, the list works.
const fakeCurl = `#!/bin/sh
for last; do :; done
case "$last" in
  */releases/latest)
    [ "$FAKE_STABLE" = "1" ] && { printf '{"tag_name": "v1.0.0", "prerelease": false}\n'; exit 0; }
    exit 22 ;;
  *releases?per_page=1)
    [ "$FAKE_EMPTY" = "1" ] && { printf '[]\n'; exit 0; }
    printf '[{"url": "x", "tag_name": "v0.1.0-beta.2", "prerelease": true}]\n'; exit 0 ;;
esac
exit 22
`

// resolve sources install.sh as a library and prints what resolve_tag chooses.
func resolve(t *testing.T, env ...string) (string, error) {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh available")
	}
	if runtime.GOOS == "windows" {
		t.Skip("shell tests run on Linux CI")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "curl"), []byte(fakeCurl), 0o700); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatal(err)
	}
	script, err := filepath.Abs(filepath.Join("..", "..", installPath))
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.CommandContext(t.Context(), sh, "-c", `. "$1"; resolve_tag`, "sh", script) //nolint:gosec // fixed arguments
	cmd.Env = append(os.Environ(), append([]string{"PATH=" + dir + ":" + os.Getenv("PATH"), "KEST_INSTALL_LIB=1"}, env...)...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func TestInstallerResolvesTags(t *testing.T) {
	tests := []struct {
		name string
		env  []string
		want string
		fail bool
	}{
		{"explicit tag is used as given", []string{"KEST_VERSION=v0.1.0-beta.1"}, "v0.1.0-beta.1", false},
		{"latest prefers the stable release", []string{"KEST_VERSION=latest", "FAKE_STABLE=1"}, "v1.0.0", false},
		{"latest falls back to the newest pre-release", []string{"KEST_VERSION=latest"}, "v0.1.0-beta.2", false},
		{"unset means latest", nil, "v0.1.0-beta.2", false},
		{"no releases at all is an error", []string{"KEST_VERSION=latest", "FAKE_EMPTY=1"}, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolve(t, tt.env...)
			if tt.fail {
				if err == nil {
					t.Fatalf("resolve_tag succeeded with %q, want failure", got)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("resolve_tag = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

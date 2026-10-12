package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// SecretsFileName is the secrets file inside the data directory.
const SecretsFileName = "secrets.json"

// SecretsPath returns where the secrets file lives for the data directory the
// given lookup resolves to.
func SecretsPath(lookup LookupFunc) string {
	dir := Defaults().DataDir
	if d, ok := lookup(envDataDir); ok && d != "" {
		dir = d
	}
	return filepath.Join(dir, SecretsFileName)
}

// WithSecretsFile returns a lookup that falls back to the secrets file at path
// for variables the given lookup does not set. The file is a flat JSON object
// of variable names to values, for example {"KEST_OPENROUTER_API_KEY": "..."}.
//
// A missing file is fine. On systems with Unix permissions the file must not be
// readable by group or others (mode 0600), and Kest refuses to start otherwise,
// because a secret in a world-readable file is already leaked.
func WithSecretsFile(path string, lookup LookupFunc) (LookupFunc, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return lookup, nil
	}
	if err != nil {
		return nil, fmt.Errorf("check secrets file: %w", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("secrets file %s is readable by other users (mode %04o); run: chmod 600 %s",
			path, info.Mode().Perm(), path)
	}

	data, err := os.ReadFile(path) //nolint:gosec // the operator's own secrets file
	if err != nil {
		return nil, fmt.Errorf("read secrets file: %w", err)
	}
	var vars map[string]string
	if err := json.Unmarshal(data, &vars); err != nil {
		return nil, fmt.Errorf("secrets file %s must be a JSON object of strings: %w", path, err)
	}
	return func(key string) (string, bool) {
		if v, ok := lookup(key); ok {
			return v, true
		}
		v, ok := vars[key]
		return v, ok
	}, nil
}

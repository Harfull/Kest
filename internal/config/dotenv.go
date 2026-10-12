package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// WithDotEnv returns a lookup that reads KEST_* style variables from the env
// file at path when the real environment does not set them. A missing file is
// not an error: hosts such as Pterodactyl provide real variables and have no
// file. The process environment is never modified.
func WithDotEnv(path string, lookup LookupFunc) (LookupFunc, error) {
	vars, err := readDotEnv(path)
	if err != nil {
		return nil, err
	}
	return func(key string) (string, bool) {
		if v, ok := lookup(key); ok {
			return v, true
		}
		v, ok := vars[key]
		return v, ok
	}, nil
}

// readDotEnv parses lines of KEY=VALUE. Blank lines and lines starting with #
// are skipped, an optional "export " prefix is allowed, and one pair of
// matching quotes around a value is removed.
func readDotEnv(path string) (map[string]string, error) {
	f, err := os.Open(path) //nolint:gosec // path is the operator's own env file
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open env file: %w", err)
	}
	defer f.Close() //nolint:errcheck // read-only file

	vars := map[string]string{}
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), bom))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, val, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, fmt.Errorf("env file line %d: want KEY=VALUE", n)
		}
		vars[key] = unquote(strings.TrimSpace(val))
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read env file: %w", err)
	}
	return vars, nil
}

// bom is the byte order mark some Windows editors put at the start of a file.
const bom = string(rune(0xFEFF))

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

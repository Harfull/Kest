package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWithDotEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	content := bom + "# comment\r\nKEST_MODEL_GENERAL=vendor/model\r\n\r\nexport KEST_OWNER_ID = 42\nKEST_TTS_VOICE=\"Kore\"\nKEST_TIMEZONE='UTC'\nKEST_FROM_ENV=file\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	real := func(k string) (string, bool) {
		if k == "KEST_FROM_ENV" {
			return "process", true
		}
		return "", false
	}

	lookup, err := WithDotEnv(path, real)
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string]string{
		"KEST_MODEL_GENERAL": "vendor/model",
		"KEST_OWNER_ID":      "42",
		"KEST_TTS_VOICE":     "Kore",
		"KEST_TIMEZONE":      "UTC",
		"KEST_FROM_ENV":      "process", // the real environment wins
	}
	for key, want := range tests {
		if got, ok := lookup(key); !ok || got != want {
			t.Errorf("lookup(%s) = %q, %v; want %q", key, got, ok, want)
		}
	}
	if _, ok := lookup("KEST_ABSENT"); ok {
		t.Error("an unset variable was reported as set")
	}
}

func TestWithDotEnvMissingFileIsFine(t *testing.T) {
	lookup, err := WithDotEnv(filepath.Join(t.TempDir(), "none"), func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("WithDotEnv() = %v", err)
	}
	if _, ok := lookup("X"); ok {
		t.Error("found a variable that does not exist")
	}
}

func TestWithDotEnvRejectsBrokenLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("not a pair\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := WithDotEnv(path, os.LookupEnv); err == nil {
		t.Error("a line without = was accepted")
	}
}

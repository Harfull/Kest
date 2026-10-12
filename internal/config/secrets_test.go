package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func writeSecrets(t *testing.T, content string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), SecretsFileName)
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil { // WriteFile is subject to the umask
		t.Fatal(err)
	}
	return path
}

func TestWithSecretsFile(t *testing.T) {
	path := writeSecrets(t, `{"KEST_OPENROUTER_API_KEY":"from-file","KEST_OWNER_ID":"1"}`, 0o600)
	env := func(k string) (string, bool) {
		if k == "KEST_OWNER_ID" {
			return "2", true
		}
		return "", false
	}

	lookup, err := WithSecretsFile(path, env)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := lookup("KEST_OPENROUTER_API_KEY"); !ok || v != "from-file" {
		t.Errorf("file value = %q, %v", v, ok)
	}
	if v, _ := lookup("KEST_OWNER_ID"); v != "2" {
		t.Errorf("the environment should win, got %q", v)
	}
	if _, ok := lookup("KEST_ABSENT"); ok {
		t.Error("an absent variable was reported as set")
	}
}

func TestWithSecretsFileMissingIsFine(t *testing.T) {
	lookup, err := WithSecretsFile(filepath.Join(t.TempDir(), "none.json"), func(string) (string, bool) { return "", false })
	if err != nil || lookup == nil {
		t.Fatalf("WithSecretsFile() = %v, %v", lookup, err)
	}
}

func TestWithSecretsFileRefusesOpenPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no Unix permission bits")
	}
	path := writeSecrets(t, `{"A":"b"}`, 0o644)
	if _, err := WithSecretsFile(path, os.LookupEnv); err == nil {
		t.Error("a world-readable secrets file was accepted")
	}
}

func TestWithSecretsFileRejectsBadContent(t *testing.T) {
	for _, content := range []string{`not json`, `["a"]`, `{"A":1}`} {
		path := writeSecrets(t, content, 0o600)
		if _, err := WithSecretsFile(path, os.LookupEnv); err == nil {
			t.Errorf("content %q was accepted", content)
		}
	}
}

func TestSecretsPathFollowsTheDataDir(t *testing.T) {
	lookup := func(k string) (string, bool) {
		if k == envDataDir {
			return "/srv/kest", true
		}
		return "", false
	}
	if got, want := SecretsPath(lookup), filepath.Join("/srv/kest", SecretsFileName); got != want {
		t.Errorf("SecretsPath() = %q, want %q", got, want)
	}
}

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	_ "time/tzdata" // zone data for tests on hosts without a system database
)

// env returns a LookupFunc backed by a map.
func env(m map[string]string) LookupFunc {
	return func(key string) (string, bool) {
		v, ok := m[key]
		return v, ok
	}
}

// minimalEnv is the smallest environment that produces a valid config.
func minimalEnv() map[string]string {
	return map[string]string{
		envTelegramToken: "123:abc",
		envOwnerID:       "42",
		envOpenRouterKey: "sk-or-test",
	}
}

func TestLoadDefaultsWithMinimalEnv(t *testing.T) {
	cfg, err := Load("", env(minimalEnv()))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	want := Defaults()
	if cfg.Timezone != want.Timezone {
		t.Errorf("Timezone = %q, want %q", cfg.Timezone, want.Timezone)
	}
	if cfg.Coach.MaxProactivePerDay != 3 {
		t.Errorf("MaxProactivePerDay = %d, want 3", cfg.Coach.MaxProactivePerDay)
	}
	if got := cfg.Telegram.OwnerIDs; len(got) != 1 || got[0] != 42 {
		t.Errorf("OwnerIDs = %v, want [42]", got)
	}
}

func TestEnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	file := `
timezone = "Europe/London"

[coach]
max_proactive_per_day = 5
quiet_start = "22:30"

[llm.models]
general = "from/file"
`
	if err := os.WriteFile(path, []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}

	e := minimalEnv()
	e[envModelGeneral] = "from/env"
	e[envMaxProactive] = "2"

	cfg, err := Load(path, env(e))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	tests := []struct {
		name string
		got  any
		want any
	}{
		{"timezone from file", cfg.Timezone, "Europe/London"},
		{"quiet start from file", cfg.Coach.QuietStart, "22:30"},
		{"model: env beats file", cfg.LLM.Models.General, "from/env"},
		{"max proactive: env beats file", cfg.Coach.MaxProactivePerDay, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %v, want %v", tt.got, tt.want)
			}
		})
	}
}

func TestBlankEnvDoesNotEraseFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(`timezone = "Asia/Tokyo"`), 0o600); err != nil {
		t.Fatal(err)
	}
	e := minimalEnv()
	e[envTimezone] = "  "

	cfg, err := Load(path, env(e))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Timezone != "Asia/Tokyo" {
		t.Errorf("Timezone = %q, want Asia/Tokyo", cfg.Timezone)
	}
}

func TestMissingFileIsNotAnError(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "absent.toml"), env(minimalEnv()))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
}

func TestUnknownFileKeyIsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("timezoen = \"UTC\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path, env(minimalEnv()))
	if err == nil {
		t.Fatal("Load() succeeded with an unknown key, want error")
	}
}

func TestValidateReportsEveryProblem(t *testing.T) {
	cfg := Defaults()
	cfg.Timezone = "Mars/Olympus"
	cfg.Log.Level = "loud"
	cfg.Coach.QuietStart = "25:00"

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want error")
	}

	msg := err.Error()
	for _, want := range []string{
		"timezone",
		"log.level",
		"telegram token",
		"owner id",
		"OpenRouter API key",
		"coach.quiet_start",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error does not mention %q:\n%s", want, msg)
		}
	}
}

func TestEnvParsingErrors(t *testing.T) {
	tests := []struct {
		name string
		key  string
		val  string
	}{
		{"owner id not numeric", envOwnerID, "abc"},
		{"budget not a number", envDailyBudget, "lots"},
		{"max proactive not an integer", envMaxProactive, "1.5"},
		{"zdr not a boolean", envZDROnly, "maybe"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := minimalEnv()
			e[tt.key] = tt.val
			if _, err := Load("", env(e)); err == nil {
				t.Fatal("Load() succeeded, want error")
			}
		})
	}
}

func TestMultipleOwnersAndFallbacks(t *testing.T) {
	e := minimalEnv()
	e[envOwnerID] = "1, 2 ,3"
	e[envModelFallbacks] = "a/one, b/two,,"

	cfg, err := Load("", env(e))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := fmt.Sprint(cfg.Telegram.OwnerIDs); got != "[1 2 3]" {
		t.Errorf("OwnerIDs = %s, want [1 2 3]", got)
	}
	if got := fmt.Sprint(cfg.LLM.Models.Fallbacks); got != "[a/one b/two]" {
		t.Errorf("Fallbacks = %s, want [a/one b/two]", got)
	}
}

func TestSecretNeverPrints(t *testing.T) {
	s := Secret("super-secret-value")

	for _, got := range []string{
		fmt.Sprint(s),
		fmt.Sprintf("%v", s),
		fmt.Sprintf("%+v", s),
		fmt.Sprintf("%#v", s),
		fmt.Sprintf("secret=%s", s),
	} {
		if strings.Contains(got, "super-secret") {
			t.Errorf("secret leaked in %q", got)
		}
	}

	cfg := Defaults()
	cfg.Telegram.Token = s
	if got := fmt.Sprintf("%+v", cfg); strings.Contains(got, "super-secret") {
		t.Errorf("secret leaked via struct formatting: %s", got)
	}

	if s.Reveal() != "super-secret-value" {
		t.Error("Reveal() did not return the value")
	}
}

func TestPath(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"default", nil, filepath.Join("data", "config.toml")},
		{"data dir", map[string]string{envDataDir: "/srv/kest"}, filepath.Join("/srv/kest", "config.toml")},
		{"explicit config wins", map[string]string{envConfig: "/etc/kest.toml", envDataDir: "/srv"}, "/etc/kest.toml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Path(env(tt.env)); got != tt.want {
				t.Errorf("Path() = %q, want %q", got, tt.want)
			}
		})
	}
}

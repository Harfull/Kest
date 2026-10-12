package config

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// repoFile returns the path of a file at the repository root.
func repoFile(name string) string {
	return filepath.Join("..", "..", name)
}

func TestExampleConfigLoads(t *testing.T) {
	// The example file must stay valid: unknown keys and bad values fail here.
	if _, err := Load(repoFile("config.example.toml"), env(minimalEnv())); err != nil {
		t.Fatalf("config.example.toml does not load: %v", err)
	}
}

func TestEnvExampleListsEveryVariable(t *testing.T) {
	data, err := os.ReadFile(repoFile(".env.example"))
	if err != nil {
		t.Fatal(err)
	}
	example := string(data)

	for _, name := range []string{
		envConfig, envDataDir, envTimezone, envLogLevel, envLogFormat, envLogSensitive,
		envTelegramToken, envOwnerID, envOpenRouterKey, envOpenRouterURL,
		envModelGeneral, envModelFallbacks, envModelTranscr, envModelTTS, envTTSVoice,
		envDailyBudget, envZDROnly, envMaxProactive, envQuietStart, envQuietEnd, envPlugins,
		envBrowserModule, envBrowserHeadless, envBrowserSandbox,
	} {
		if !regexp.MustCompile(`(?m)^#?\s*` + regexp.QuoteMeta(name) + `=`).MatchString(example) {
			t.Errorf(".env.example is missing %s", name)
		}
	}
}

func TestEggVariablesMatchConfig(t *testing.T) {
	// Every KEST_* variable the egg defines must be one the config reads.
	data, err := os.ReadFile(repoFile("scripts/sync-egg/main.go"))
	if err != nil {
		t.Fatal(err)
	}

	known := map[string]bool{
		envBrowserModule: true, envBrowserHeadless: true, envBrowserSandbox: true,
		envDataDir: true, envTimezone: true, envLogLevel: true,
		envTelegramToken: true, envOwnerID: true, envOpenRouterKey: true,
		envModelGeneral: true, envModelFallbacks: true, envModelTranscr: true,
		envModelTTS: true, envTTSVoice: true, envDailyBudget: true, envMaxProactive: true, envPlugins: true,
		"KEST_VERSION": true, // read by the installer, not by the binary
	}

	for _, m := range regexp.MustCompile(`EnvVariable:\s+"(KEST_[A-Z_]+)"`).FindAllStringSubmatch(string(data), -1) {
		if !known[m[1]] {
			t.Errorf("egg defines %s, which the config does not read", m[1])
		}
	}
}

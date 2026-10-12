// Package config loads, layers and validates Kest's configuration.
//
// Layers, lowest to highest precedence: built-in defaults, config.toml in the
// data directory, then KEST_* environment variables. Validation runs once and
// reports every problem together.
//
// It does not read secrets from anywhere other than those layers, and it never
// logs values.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Secret is a string that never prints its value.
type Secret string

// String implements fmt.Stringer and returns a placeholder.
func (Secret) String() string { return "[redacted]" }

// GoString implements fmt.GoStringer so %#v does not leak the value.
func (Secret) GoString() string { return "config.Secret([redacted])" }

// MarshalText implements encoding.TextMarshaler and returns a placeholder.
func (Secret) MarshalText() ([]byte, error) { return []byte("[redacted]"), nil }

// Reveal returns the underlying value. Call it only at the point of use.
func (s Secret) Reveal() string { return string(s) }

// IsSet reports whether the secret has a value.
func (s Secret) IsSet() bool { return s != "" }

// Config is the complete runtime configuration.
type Config struct {
	DataDir  string         `toml:"data_dir"`
	Timezone string         `toml:"timezone"`
	Log      LogConfig      `toml:"log"`
	Telegram TelegramConfig `toml:"telegram"`
	LLM      LLMConfig      `toml:"llm"`
	Coach    CoachConfig    `toml:"coach"`

	// EnabledPlugins lists the built-in plugins to load, by id.
	EnabledPlugins []string `toml:"enabled_plugins"`
	// Plugins holds each plugin's settings under [plugins.<id>]. They are
	// validated against the plugin manifest when it loads.
	Plugins map[string]map[string]any `toml:"plugins"`
}

// LogConfig controls logging output.
type LogConfig struct {
	// Level is debug, info, warn or error.
	Level string `toml:"level"`
	// Format is text or json.
	Format string `toml:"format"`
	// Sensitive allows message content in debug logs. Off by default.
	Sensitive bool `toml:"sensitive"`
}

// TelegramConfig configures the Telegram transport.
type TelegramConfig struct {
	Token Secret `toml:"token"`
	// OwnerIDs lists the only Telegram user IDs Kest will talk to.
	OwnerIDs []int64 `toml:"owner_ids"`
}

// LLMConfig configures OpenRouter access and model choice.
type LLMConfig struct {
	APIKey  Secret `toml:"api_key"`
	BaseURL string `toml:"base_url"`
	Models  Models `toml:"models"`
	// TTSVoice is the voice name passed to the speech model.
	TTSVoice string `toml:"tts_voice"`
	// DailyBudgetUSD stops proactive messages and costly calls when exceeded.
	// Zero disables the guard.
	DailyBudgetUSD float64 `toml:"daily_budget_usd"`
	// ZDROnly restricts routing to zero-data-retention providers where supported.
	ZDROnly bool `toml:"zdr_only"`
}

// Models names the three model slots.
type Models struct {
	General       string   `toml:"general"`
	Fallbacks     []string `toml:"fallbacks"`
	Transcription string   `toml:"transcription"`
	TTS           string   `toml:"tts"`
}

// CoachConfig holds coaching defaults that apply before the sleep model has
// enough data and that the owner can override at any time.
type CoachConfig struct {
	// MaxProactivePerDay caps unsolicited messages per day.
	MaxProactivePerDay int `toml:"max_proactive_per_day"`
	// QuietStart and QuietEnd are local HH:MM times used until the sleep model
	// has enough data.
	QuietStart string `toml:"quiet_start"`
	QuietEnd   string `toml:"quiet_end"`
}

// Defaults returns the built-in configuration.
//
// Model identifiers are examples taken from OpenRouter's documentation and
// change over time. Verify them against https://openrouter.ai/models.
func Defaults() Config {
	return Config{
		DataDir:  "./data",
		Timezone: "UTC",
		Log:      LogConfig{Level: "info", Format: "text"},
		LLM: LLMConfig{
			BaseURL: "https://openrouter.ai/api/v1",
			Models: Models{
				General:       "anthropic/claude-haiku-5.5",
				Transcription: "openai/gpt-4o-mini-transcribe",
				TTS:           "google/gemini-3.1-flash-tts-preview",
			},
			TTSVoice:       "Kore",
			DailyBudgetUSD: 1.00,
		},
		EnabledPlugins: []string{"reminders", "tasks", "webfetch"},
		Coach: CoachConfig{
			MaxProactivePerDay: 3,
			QuietStart:         "23:00",
			QuietEnd:           "07:00",
		},
	}
}

// Path returns the config file location: KEST_CONFIG if set, otherwise
// config.toml inside the data directory.
func Path(lookup LookupFunc) string {
	if p, ok := lookup(envConfig); ok && p != "" {
		return p
	}
	dir := Defaults().DataDir
	if d, ok := lookup(envDataDir); ok && d != "" {
		dir = d
	}
	return filepath.Join(dir, "config.toml")
}

// Load builds the configuration from defaults, the TOML file at path (skipped
// if it does not exist) and environment overrides, then validates it.
func Load(path string, lookup LookupFunc) (Config, error) {
	cfg := Defaults()

	if path != "" {
		if err := loadFile(path, &cfg); err != nil {
			return Config{}, err
		}
	}

	if err := applyEnv(&cfg, lookup); err != nil {
		return Config{}, err
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func loadFile(path string, cfg *Config) error {
	f, err := os.Open(path) //nolint:gosec // path comes from an operator-controlled flag or env var
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open config file: %w", err)
	}
	defer f.Close() //nolint:errcheck // read-only file

	if err := decodeTOML(f, cfg); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}

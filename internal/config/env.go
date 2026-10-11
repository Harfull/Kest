package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Environment variable names. All carry the KEST_ prefix.
const (
	envConfig         = "KEST_CONFIG"
	envDataDir        = "KEST_DATA_DIR"
	envTimezone       = "KEST_TIMEZONE"
	envLogLevel       = "KEST_LOG_LEVEL"
	envLogFormat      = "KEST_LOG_FORMAT"
	envLogSensitive   = "KEST_LOG_SENSITIVE"
	envTelegramToken  = "KEST_TELEGRAM_TOKEN"
	envOwnerID        = "KEST_OWNER_ID"
	envOpenRouterKey  = "KEST_OPENROUTER_API_KEY"
	envOpenRouterURL  = "KEST_OPENROUTER_BASE_URL"
	envModelGeneral   = "KEST_MODEL_GENERAL"
	envModelFallbacks = "KEST_MODEL_FALLBACKS"
	envModelTranscr   = "KEST_MODEL_TRANSCRIPTION"
	envModelTTS       = "KEST_MODEL_TTS"
	envTTSVoice       = "KEST_TTS_VOICE"
	envDailyBudget    = "KEST_DAILY_BUDGET_USD"
	envZDROnly        = "KEST_ZDR_ONLY"
	envMaxProactive   = "KEST_MAX_PROACTIVE_PER_DAY"
	envQuietStart     = "KEST_QUIET_START"
	envQuietEnd       = "KEST_QUIET_END"
)

// LookupFunc reads an environment variable. os.LookupEnv satisfies it.
type LookupFunc func(key string) (string, bool)

// applyEnv overlays KEST_* variables onto cfg. Empty values are ignored so a
// blank panel field does not erase a value from the config file.
func applyEnv(cfg *Config, lookup LookupFunc) error {
	var errs []error
	get := func(key string) (string, bool) {
		v, ok := lookup(key)
		v = strings.TrimSpace(v)
		return v, ok && v != ""
	}
	str := func(key string, dst *string) {
		if v, ok := get(key); ok {
			*dst = v
		}
	}
	boolean := func(key string, dst *bool) {
		if v, ok := get(key); ok {
			b, err := strconv.ParseBool(v)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: not a boolean: %q", key, v))
				return
			}
			*dst = b
		}
	}

	str(envDataDir, &cfg.DataDir)
	str(envTimezone, &cfg.Timezone)
	str(envLogLevel, &cfg.Log.Level)
	str(envLogFormat, &cfg.Log.Format)
	boolean(envLogSensitive, &cfg.Log.Sensitive)

	if v, ok := get(envTelegramToken); ok {
		cfg.Telegram.Token = Secret(v)
	}
	if v, ok := get(envOwnerID); ok {
		ids, err := parseIDs(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", envOwnerID, err))
		} else {
			cfg.Telegram.OwnerIDs = ids
		}
	}

	if v, ok := get(envOpenRouterKey); ok {
		cfg.LLM.APIKey = Secret(v)
	}
	str(envOpenRouterURL, &cfg.LLM.BaseURL)
	str(envModelGeneral, &cfg.LLM.Models.General)
	if v, ok := get(envModelFallbacks); ok {
		cfg.LLM.Models.Fallbacks = splitList(v)
	}
	str(envModelTranscr, &cfg.LLM.Models.Transcription)
	str(envModelTTS, &cfg.LLM.Models.TTS)
	str(envTTSVoice, &cfg.LLM.TTSVoice)
	boolean(envZDROnly, &cfg.LLM.ZDROnly)

	if v, ok := get(envDailyBudget); ok {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: not a number: %q", envDailyBudget, v))
		} else {
			cfg.LLM.DailyBudgetUSD = f
		}
	}

	if v, ok := get(envMaxProactive); ok {
		n, err := strconv.Atoi(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: not an integer: %q", envMaxProactive, v))
		} else {
			cfg.Coach.MaxProactivePerDay = n
		}
	}
	str(envQuietStart, &cfg.Coach.QuietStart)
	str(envQuietEnd, &cfg.Coach.QuietEnd)

	return errors.Join(errs...)
}

// parseIDs parses a comma-separated list of Telegram user IDs.
func parseIDs(s string) ([]int64, error) {
	parts := splitList(s)
	ids := make([]int64, 0, len(parts))
	for _, p := range parts {
		id, err := strconv.ParseInt(p, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("not a numeric id: %q", p)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// splitList splits a comma-separated list, trimming blanks.
func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

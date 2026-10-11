package config

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

var (
	validLevels  = []string{"debug", "info", "warn", "error"}
	validFormats = []string{"text", "json"}
)

// Validate reports every problem with cfg at once.
func (c Config) Validate() error {
	var errs []error
	add := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf(format, args...))
	}

	if strings.TrimSpace(c.DataDir) == "" {
		add("data_dir must not be empty")
	}

	if _, err := time.LoadLocation(c.Timezone); err != nil || c.Timezone == "" {
		add("timezone %q is not a valid IANA zone (example: Europe/London)", c.Timezone)
	}

	if !slices.Contains(validLevels, strings.ToLower(c.Log.Level)) {
		add("log.level %q must be one of %s", c.Log.Level, strings.Join(validLevels, ", "))
	}
	if !slices.Contains(validFormats, strings.ToLower(c.Log.Format)) {
		add("log.format %q must be one of %s", c.Log.Format, strings.Join(validFormats, ", "))
	}

	if !c.Telegram.Token.IsSet() {
		add("telegram token is required (set %s)", envTelegramToken)
	}
	if len(c.Telegram.OwnerIDs) == 0 {
		add("at least one owner id is required (set %s)", envOwnerID)
	}
	for _, id := range c.Telegram.OwnerIDs {
		if id <= 0 {
			add("owner id %d must be positive", id)
		}
	}

	if !c.LLM.APIKey.IsSet() {
		add("OpenRouter API key is required (set %s)", envOpenRouterKey)
	}
	if strings.TrimSpace(c.LLM.BaseURL) == "" {
		add("llm.base_url must not be empty")
	}
	for _, m := range []struct{ name, value string }{
		{"llm.models.general", c.LLM.Models.General},
		{"llm.models.transcription", c.LLM.Models.Transcription},
		{"llm.models.tts", c.LLM.Models.TTS},
	} {
		if strings.TrimSpace(m.value) == "" {
			add("%s must not be empty", m.name)
		}
	}
	if c.LLM.DailyBudgetUSD < 0 {
		add("llm.daily_budget_usd must not be negative")
	}

	if c.Coach.MaxProactivePerDay < 0 || c.Coach.MaxProactivePerDay > 20 {
		add("coach.max_proactive_per_day %d must be between 0 and 20", c.Coach.MaxProactivePerDay)
	}
	for _, q := range []struct{ name, value string }{
		{"coach.quiet_start", c.Coach.QuietStart},
		{"coach.quiet_end", c.Coach.QuietEnd},
	} {
		if _, err := time.Parse("15:04", q.value); err != nil {
			add("%s %q must be HH:MM in 24-hour time", q.name, q.value)
		}
	}

	return errors.Join(errs...)
}

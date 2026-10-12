package config

import "testing"

func TestBrowserEnvironmentOverridesScopedConfig(t *testing.T) {
	for _, tt := range []struct {
		name, enabled, sandbox string
		wantSetting, wantError bool
	}{
		{"enabled", "tasks,browser", "true", true, false},
		{"disabled", "tasks", "true", false, false},
		{"invalid boolean", "browser", "perhaps", false, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Defaults()
			cfg.Plugins = map[string]map[string]any{"browser": {"sandbox": false}}
			err := applyEnv(&cfg, env(map[string]string{envPlugins: tt.enabled, envBrowserSandbox: tt.sandbox}))
			if (err != nil) != tt.wantError {
				t.Fatalf("error = %v", err)
			}
			if !tt.wantError && cfg.Plugins["browser"]["sandbox"] != tt.wantSetting {
				t.Fatalf("sandbox = %v", cfg.Plugins["browser"]["sandbox"])
			}
		})
	}
}

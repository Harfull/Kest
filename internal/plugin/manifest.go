package plugin

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Manifest describes a plugin: who it is, what it may touch and which tools it
// offers. Native plugins embed theirs as manifest.toml.
type Manifest struct {
	ID           string                 `toml:"id"`
	Name         string                 `toml:"name"`
	Version      string                 `toml:"version"`
	Description  string                 `toml:"description"`
	Capabilities Capabilities           `toml:"capabilities"`
	Config       map[string]ConfigField `toml:"config"`
	Tools        []ManifestTool         `toml:"tools"`
}

// Capabilities declares what a plugin is allowed to reach.
type Capabilities struct {
	// Network lists hosts the plugin may contact. "*" means any public host;
	// private and loopback addresses are always refused.
	Network []string `toml:"network"`
	// Secrets lists secret names the plugin may read through Host.Secret.
	Secrets []string `toml:"secrets"`
	// Jobs declares that the plugin schedules work and implements JobProvider.
	Jobs bool `toml:"jobs"`
}

// ConfigField describes one setting under [plugins.<id>] in config.toml.
type ConfigField struct {
	// Type is "string", "int", "float" or "bool".
	Type     string `toml:"type"`
	Default  any    `toml:"default"`
	Required bool   `toml:"required"`
}

// ManifestTool declares a tool and its risk. The code must agree.
type ManifestTool struct {
	Name string `toml:"name"`
	Risk string `toml:"risk"`
}

// ParseManifest decodes and validates a manifest.toml. Unknown keys are errors.
func ParseManifest(data []byte) (Manifest, error) {
	var m Manifest
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("parse manifest: %w", err)
	}
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// Validate checks the manifest for internal consistency.
func (m Manifest) Validate() error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if !pluginIDRe.MatchString(m.ID) {
		add("id %q must be lower-case letters, digits and hyphens, starting with a letter", m.ID)
	}
	if strings.TrimSpace(m.Name) == "" {
		add("name is required")
	}
	if strings.TrimSpace(m.Version) == "" {
		add("version is required")
	}
	if strings.TrimSpace(m.Description) == "" {
		add("description is required")
	}

	seen := map[string]bool{}
	for _, t := range m.Tools {
		if !toolNameRe.MatchString(t.Name) {
			add("tool name %q is invalid", t.Name)
		}
		if seen[t.Name] {
			add("tool %q is declared twice", t.Name)
		}
		seen[t.Name] = true
		if _, err := ParseRisk(t.Risk); err != nil {
			add("tool %s: %v", t.Name, err)
		}
	}

	for name, f := range m.Config {
		if !slices.Contains([]string{"string", "int", "float", "bool"}, f.Type) {
			add("config %q: type %q must be string, int, float or bool", name, f.Type)
			continue
		}
		if f.Default != nil {
			if _, err := coerce(f.Type, f.Default); err != nil {
				add("config %q: default: %v", name, err)
			}
		}
	}
	return errors.Join(errs...)
}

// risk returns the declared risk of the named tool.
func (m Manifest) risk(tool string) (Risk, bool) {
	for _, t := range m.Tools {
		if t.Name == tool {
			r, err := ParseRisk(t.Risk)
			return r, err == nil
		}
	}
	return 0, false
}

// Config holds a plugin's resolved settings.
type Config map[string]any

// String returns a string setting, or "" if unset.
func (c Config) String(key string) string { s, _ := c[key].(string); return s }

// Int returns an integer setting, or 0 if unset.
func (c Config) Int(key string) int { n, _ := c[key].(int); return n }

// Float returns a float setting, or 0 if unset.
func (c Config) Float(key string) float64 { f, _ := c[key].(float64); return f }

// Bool returns a bool setting, or false if unset.
func (c Config) Bool(key string) bool { b, _ := c[key].(bool); return b }

// ResolveConfig validates raw settings against the manifest, applies defaults
// and converts TOML numbers to the declared types.
func (m Manifest) ResolveConfig(raw map[string]any) (Config, error) {
	cfg := Config{}
	var errs []error

	for key := range raw {
		if _, ok := m.Config[key]; !ok {
			errs = append(errs, fmt.Errorf("unknown setting %q", key))
		}
	}
	for key, field := range m.Config {
		v, present := raw[key]
		if !present {
			v = field.Default
		}
		if v == nil {
			if field.Required {
				errs = append(errs, fmt.Errorf("setting %q is required", key))
			}
			continue
		}
		cv, err := coerce(field.Type, v)
		if err != nil {
			errs = append(errs, fmt.Errorf("setting %q: %w", key, err))
			continue
		}
		cfg[key] = cv
	}
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("plugin %s config: %w", m.ID, err)
	}
	return cfg, nil
}

// coerce converts a TOML-decoded value to the manifest type.
func coerce(typ string, v any) (any, error) {
	switch typ {
	case "string":
		if s, ok := v.(string); ok {
			return s, nil
		}
	case "int":
		switch n := v.(type) {
		case int64:
			return int(n), nil
		case int:
			return n, nil
		}
	case "float":
		switch n := v.(type) {
		case float64:
			return n, nil
		case int64:
			return float64(n), nil
		case int:
			return float64(n), nil
		}
	case "bool":
		if b, ok := v.(bool); ok {
			return b, nil
		}
	}
	return nil, fmt.Errorf("want %s, got %T", typ, v)
}

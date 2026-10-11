package config

import (
	"io"

	"github.com/pelletier/go-toml/v2"
)

// decodeTOML decodes r into cfg and rejects unknown keys so typos surface
// at startup instead of being silently ignored.
func decodeTOML(r io.Reader, cfg *Config) error {
	dec := toml.NewDecoder(r)
	dec.DisallowUnknownFields()
	return dec.Decode(cfg)
}

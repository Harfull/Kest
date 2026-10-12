// Package plugin is Kest's extension framework: the interfaces plugins
// implement, the manifest that describes them, and the registry that loads
// them and exposes their tools to the agent.
//
// It defines contracts only. It does not decide when a tool may run (the agent's
// policy does) and it knows nothing about Telegram, the model or the coach.
// Plugins import this package and nothing else from internal/.
package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Risk classifies what a tool can do. The agent's policy keys off it.
type Risk int

// Risk levels, lowest to highest.
const (
	// RiskRead only reads or computes. It runs without asking.
	RiskRead Risk = iota
	// RiskWrite changes state that the owner can easily undo, such as adding a task.
	RiskWrite
	// RiskDestructive deletes data, or cannot be undone. It is always confirmed.
	RiskDestructive
)

// String returns the manifest spelling of r.
func (r Risk) String() string {
	switch r {
	case RiskRead:
		return "read"
	case RiskWrite:
		return "write"
	case RiskDestructive:
		return "destructive"
	}
	return fmt.Sprintf("risk(%d)", int(r))
}

// ParseRisk parses "read", "write" or "destructive".
func ParseRisk(s string) (Risk, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "read":
		return RiskRead, nil
	case "write":
		return RiskWrite, nil
	case "destructive":
		return RiskDestructive, nil
	}
	return 0, fmt.Errorf("unknown risk %q (want read, write or destructive)", s)
}

// Call is one invocation of a tool.
type Call struct {
	ID   string          // the model's tool call ID, for logs
	Args json.RawMessage // arguments as a JSON object, already checked to be valid JSON
}

// Result is what a tool returns to the model.
type Result struct {
	// Content is the text the model sees.
	Content string
	// Untrusted marks content that came from outside the owner and Kest: an
	// email body, a web page, a third-party API. The agent wraps it as data and
	// treats the rest of the turn as tainted.
	Untrusted bool
	// Summary is a short human-readable line for the audit log.
	Summary string
}

// Tool is one capability a plugin offers the model.
type Tool struct {
	// Name is verb_noun in lower snake case, unique within the plugin.
	Name string
	// Description tells the model what the tool does and when to use it.
	Description string
	// Schema is a JSON Schema object describing the arguments.
	Schema json.RawMessage
	// Risk is the tool's risk level. It must match the manifest.
	Risk Risk
	// Outbound marks tools that send information to third parties (email,
	// messages, posts). They are confirmed every time, whatever Risk says.
	Outbound bool
	// Describe renders the arguments for a confirmation prompt. Optional; a
	// generic rendering is used when nil.
	Describe func(args json.RawMessage) string
	// Handler runs the tool.
	Handler func(ctx context.Context, call Call) (Result, error)
}

var (
	toolNameRe   = regexp.MustCompile(`^[a-z][a-z0-9_]{0,40}$`)
	pluginIDRe   = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	maxDescChars = 600
)

// Validate checks that the tool is well formed.
func (t Tool) Validate() error {
	if !toolNameRe.MatchString(t.Name) {
		return fmt.Errorf("tool name %q must be lower snake case, 1 to 41 characters", t.Name)
	}
	if strings.TrimSpace(t.Description) == "" || len(t.Description) > maxDescChars {
		return fmt.Errorf("tool %s: description must be 1 to %d characters", t.Name, maxDescChars)
	}
	var schema struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(t.Schema, &schema); err != nil || schema.Type != "object" {
		return fmt.Errorf("tool %s: schema must be a JSON object with type \"object\"", t.Name)
	}
	if t.Handler == nil {
		return fmt.Errorf("tool %s: no handler", t.Name)
	}
	if t.Risk < RiskRead || t.Risk > RiskDestructive {
		return fmt.Errorf("tool %s: invalid risk", t.Name)
	}
	return nil
}

// Plugin is the unit of extension.
type Plugin interface {
	// Manifest returns the plugin's description. It must be cheap and constant.
	Manifest() Manifest
	// Init prepares the plugin. Failure disables only this plugin.
	Init(ctx context.Context, host Host) error
	// Tools lists the tools to expose. Called after a successful Init.
	Tools() []Tool
	// Close releases resources.
	Close(ctx context.Context) error
}

// JobProvider is implemented by plugins that schedule work. Each job name is
// registered with the scheduler as "<plugin id>/<name>".
type JobProvider interface {
	JobNames() []string
	// RunJob handles a scheduled job. payload is what was passed to Jobs.Schedule.
	RunJob(ctx context.Context, name, payload string) error
}

// Factory builds a fresh plugin instance.
type Factory func() Plugin

// ErrNotFound is returned by KV and Jobs for missing items.
var ErrNotFound = errors.New("not found")

package plugin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Harfull/Kest/internal/clock"
)

// initTimeout bounds a plugin's Init.
const initTimeout = 30 * time.Second

// Deps are the shared services the registry hands to plugins, each scoped per
// plugin through a function so one plugin cannot reach another's data.
type Deps struct {
	Log   *slog.Logger
	Clock clock.Clock
	Loc   *time.Location
	// KV returns storage scoped to the given plugin ID.
	KV func(pluginID string) KV
	// Jobs returns job scheduling scoped to the given plugin ID.
	Jobs func(pluginID string) Jobs
	// Notifier sends unsolicited messages to the owner.
	Notifier func(ctx context.Context, n Notification) error
	// Secret looks up a secret by name. The registry only exposes names a
	// plugin's manifest declared.
	Secret func(name string) (string, bool)
	// HTTPOptions overrides HTTP client options; tests use it to allow loopback.
	HTTPOptions func(allowHosts []string) HTTPOptions
}

// ToolRef locates a tool within the registry.
type ToolRef struct {
	Plugin string
	Tool   Tool
}

// Full returns the display name, "plugin.tool".
func (r ToolRef) Full() string { return r.Plugin + "." + r.Tool.Name }

// Wire returns the name sent to the model. Function names may not contain dots.
func (r ToolRef) Wire() string { return r.Plugin + "__" + r.Tool.Name }

// Status reports how a plugin loaded.
type Status struct {
	ID      string
	Version string
	Tools   int
	Err     error // non-nil if the plugin is disabled
}

type loaded struct {
	p        Plugin
	manifest Manifest
	tools    []Tool
}

// Registry loads plugins and serves their tools.
type Registry struct {
	d Deps

	mu       sync.RWMutex
	plugins  map[string]*loaded
	statuses []Status
	byWire   map[string]ToolRef
}

// NewRegistry returns an empty Registry.
func NewRegistry(d Deps) *Registry {
	return &Registry{d: d, plugins: map[string]*loaded{}, byWire: map[string]ToolRef{}}
}

// Load builds a plugin, validates it against its manifest and initializes it.
// A failure is recorded in Status and returned; other plugins are unaffected.
func (r *Registry) Load(ctx context.Context, id string, f Factory, rawConfig map[string]any) error {
	err := r.load(ctx, id, f, rawConfig)
	if err != nil {
		r.mu.Lock()
		r.statuses = append(r.statuses, Status{ID: id, Err: err})
		r.mu.Unlock()
		r.d.Log.WarnContext(ctx, "plugin disabled", "plugin", id, "err", err)
	}
	return err
}

func (r *Registry) load(ctx context.Context, id string, f Factory, rawConfig map[string]any) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("plugin %s panicked while loading: %v", id, p)
		}
	}()

	p := f()
	m := p.Manifest()
	if err := m.Validate(); err != nil {
		return fmt.Errorf("plugin %s: invalid manifest: %w", id, err)
	}
	if m.ID != id {
		return fmt.Errorf("plugin %q registered under id %q", m.ID, id)
	}
	cfg, err := m.ResolveConfig(rawConfig)
	if err != nil {
		return err
	}

	r.mu.RLock()
	_, dup := r.plugins[id]
	r.mu.RUnlock()
	if dup {
		return fmt.Errorf("plugin %s is already loaded", id)
	}

	_, isJobs := p.(JobProvider)
	if m.Capabilities.Jobs != isJobs {
		return fmt.Errorf("plugin %s: manifest jobs=%v but JobProvider implemented=%v", id, m.Capabilities.Jobs, isJobs)
	}

	host := r.newHost(m, cfg)
	initCtx, cancel := context.WithTimeout(ctx, initTimeout)
	defer cancel()
	if err := p.Init(initCtx, host); err != nil {
		return fmt.Errorf("plugin %s: init: %w", id, err)
	}

	tools := p.Tools()
	if err := checkTools(m, tools); err != nil {
		_ = p.Close(ctx)
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.plugins[id] = &loaded{p: p, manifest: m, tools: tools}
	for _, t := range tools {
		ref := ToolRef{Plugin: id, Tool: t}
		r.byWire[ref.Wire()] = ref
	}
	r.statuses = append(r.statuses, Status{ID: id, Version: m.Version, Tools: len(tools)})
	return nil
}

// checkTools verifies that the code and the manifest describe the same tools
// with the same risk, so a manifest cannot understate what a plugin does.
func checkTools(m Manifest, tools []Tool) error {
	var errs []error
	seen := map[string]bool{}
	for _, t := range tools {
		if err := t.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("plugin %s: %w", m.ID, err))
			continue
		}
		seen[t.Name] = true
		declared, ok := m.risk(t.Name)
		switch {
		case !ok:
			errs = append(errs, fmt.Errorf("plugin %s: tool %s is not declared in the manifest", m.ID, t.Name))
		case declared != t.Risk:
			errs = append(errs, fmt.Errorf("plugin %s: tool %s has risk %s in code but %s in the manifest", m.ID, t.Name, t.Risk, declared))
		}
	}
	for _, mt := range m.Tools {
		if !seen[mt.Name] {
			errs = append(errs, fmt.Errorf("plugin %s: manifest declares tool %s but the plugin does not provide it", m.ID, mt.Name))
		}
	}
	return errors.Join(errs...)
}

// Tools returns every tool from every loaded plugin, ordered by wire name.
func (r *Registry) Tools() []ToolRef {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ToolRef, 0, len(r.byWire))
	for _, ref := range r.byWire {
		out = append(out, ref)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Wire() < out[j].Wire() })
	return out
}

// Lookup finds a tool by the name the model used.
func (r *Registry) Lookup(wire string) (ToolRef, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ref, ok := r.byWire[wire]
	return ref, ok
}

// Statuses reports how each requested plugin loaded, in load order.
func (r *Registry) Statuses() []Status {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]Status(nil), r.statuses...)
}

// JobHandlers returns scheduler handlers for plugins that provide jobs, keyed
// "<plugin id>/<job name>".
func (r *Registry) JobHandlers() map[string]func(ctx context.Context, payload string) error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := map[string]func(context.Context, string) error{}
	for id, l := range r.plugins {
		jp, ok := l.p.(JobProvider)
		if !ok {
			continue
		}
		for _, name := range jp.JobNames() {
			out[id+"/"+name] = func(ctx context.Context, payload string) error {
				return jp.RunJob(ctx, name, payload)
			}
		}
	}
	return out
}

// Close closes every plugin in reverse load order.
func (r *Registry) Close(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var errs []error
	ids := make([]string, 0, len(r.plugins))
	for id := range r.plugins {
		ids = append(ids, id)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	for _, id := range ids {
		if err := r.plugins[id].p.Close(ctx); err != nil {
			errs = append(errs, fmt.Errorf("close %s: %w", id, err))
		}
	}
	r.plugins = map[string]*loaded{}
	r.byWire = map[string]ToolRef{}
	return errors.Join(errs...)
}

// --- per-plugin host -----------------------------------------------------------

type host struct {
	r       *Registry
	m       Manifest
	cfg     Config
	log     *slog.Logger
	httpCli *http.Client
	once    sync.Once
}

func (r *Registry) newHost(m Manifest, cfg Config) *host {
	return &host{r: r, m: m, cfg: cfg, log: r.d.Log.With("plugin", m.ID)}
}

func (h *host) Logger() *slog.Logger     { return h.log }
func (h *host) Clock() clock.Clock       { return h.r.d.Clock }
func (h *host) Location() *time.Location { return h.r.d.Loc }
func (h *host) Config() Config           { return h.cfg }
func (h *host) KV() KV                   { return h.r.d.KV(h.m.ID) }
func (h *host) Jobs() Jobs               { return h.r.d.Jobs(h.m.ID) }

func (h *host) Secret(name string) (string, error) {
	declared := false
	for _, s := range h.m.Capabilities.Secrets {
		if s == name {
			declared = true
			break
		}
	}
	if !declared {
		return "", fmt.Errorf("plugin %s did not declare secret %q in its manifest", h.m.ID, name)
	}
	if h.r.d.Secret != nil {
		if v, ok := h.r.d.Secret(name); ok && v != "" {
			return v, nil
		}
	}
	return "", fmt.Errorf("secret %q is not set", name)
}

func (h *host) HTTP() *http.Client {
	h.once.Do(func() {
		opts := HTTPOptions{AllowHosts: h.m.Capabilities.Network}
		if h.r.d.HTTPOptions != nil {
			opts = h.r.d.HTTPOptions(h.m.Capabilities.Network)
		}
		h.httpCli = NewHTTPClient(opts)
	})
	return h.httpCli
}

func (h *host) Notify(ctx context.Context, n Notification) error {
	if h.r.d.Notifier == nil {
		return errors.New("no notifier configured")
	}
	n.Text = strings.TrimSpace(n.Text)
	if n.Text == "" {
		return errors.New("empty notification")
	}
	return h.r.d.Notifier(ctx, n)
}

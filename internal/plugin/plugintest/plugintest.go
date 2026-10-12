// Package plugintest provides fakes and contract checks for testing plugins
// without a running Kest.
package plugintest

import (
	"context"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Harfull/Kest/internal/clock"
	"github.com/Harfull/Kest/internal/plugin"
)

// Start is the fake clock's initial time: Sunday 11 October 2026, 14:00 UTC.
var Start = time.Date(2026, 10, 11, 14, 0, 0, 0, time.UTC)

// MemKV is an in-memory plugin.KV.
type MemKV struct {
	mu sync.Mutex
	m  map[string]string
}

// NewMemKV returns an empty store.
func NewMemKV() *MemKV { return &MemKV{m: map[string]string{}} }

// Get implements plugin.KV.
func (k *MemKV) Get(_ context.Context, key string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	v, ok := k.m[key]
	if !ok {
		return "", plugin.ErrNotFound
	}
	return v, nil
}

// Set implements plugin.KV.
func (k *MemKV) Set(_ context.Context, key, value string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.m[key] = value
	return nil
}

// Delete implements plugin.KV.
func (k *MemKV) Delete(_ context.Context, key string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.m, key)
	return nil
}

// List implements plugin.KV.
func (k *MemKV) List(_ context.Context, prefix string) ([]plugin.KVItem, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	var out []plugin.KVItem
	for key, v := range k.m {
		if strings.HasPrefix(key, prefix) {
			out = append(out, plugin.KVItem{Key: key, Value: v})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// MemJobs is an in-memory plugin.Jobs that records what was scheduled.
type MemJobs struct {
	mu   sync.Mutex
	next int
	jobs map[string]plugin.JobInfo
}

// NewMemJobs returns an empty scheduler fake.
func NewMemJobs() *MemJobs { return &MemJobs{jobs: map[string]plugin.JobInfo{}} }

// Schedule implements plugin.Jobs.
func (j *MemJobs) Schedule(_ context.Context, s plugin.JobSpec) (string, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.next++
	id := "job" + strconv.Itoa(j.next)
	j.jobs[id] = plugin.JobInfo{ID: id, Name: s.Name, Payload: s.Payload, Rule: s.Rule, NextRun: s.At}
	return id, nil
}

// Cancel implements plugin.Jobs.
func (j *MemJobs) Cancel(_ context.Context, id string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if _, ok := j.jobs[id]; !ok {
		return plugin.ErrNotFound
	}
	delete(j.jobs, id)
	return nil
}

// List implements plugin.Jobs.
func (j *MemJobs) List(context.Context) ([]plugin.JobInfo, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]plugin.JobInfo, 0, len(j.jobs))
	for _, info := range j.jobs {
		out = append(out, info)
	}
	sort.Slice(out, func(a, b int) bool {
		if !out[a].NextRun.Equal(out[b].NextRun) {
			return out[a].NextRun.Before(out[b].NextRun)
		}
		return out[a].ID < out[b].ID
	})
	return out, nil
}

// Host is a fake plugin.Host. Fields are exported so tests can set them up and
// inspect results.
type Host struct {
	Clk      *clock.Fake
	Zone     *time.Location
	Cfg      plugin.Config
	Secrets  map[string]string
	Store    *MemKV
	JobStore *MemJobs
	Client   *http.Client

	mu     sync.Mutex
	notes  []plugin.Notification
	logger *slog.Logger
}

// NewHost returns a Host with a fake clock at Start, UTC, and empty stores.
func NewHost() *Host {
	return &Host{
		Clk:      clock.NewFake(Start),
		Zone:     time.UTC,
		Cfg:      plugin.Config{},
		Secrets:  map[string]string{},
		Store:    NewMemKV(),
		JobStore: NewMemJobs(),
		Client:   plugin.NewHTTPClient(plugin.HTTPOptions{AllowHosts: []string{"*"}, AllowPrivate: true}),
		logger:   slog.New(slog.DiscardHandler),
	}
}

// Logger implements plugin.Host.
func (h *Host) Logger() *slog.Logger { return h.logger }

// Clock implements plugin.Host.
func (h *Host) Clock() clock.Clock { return h.Clk }

// Location implements plugin.Host.
func (h *Host) Location() *time.Location { return h.Zone }

// Config implements plugin.Host.
func (h *Host) Config() plugin.Config { return h.Cfg }

// KV implements plugin.Host.
func (h *Host) KV() plugin.KV { return h.Store }

// Jobs implements plugin.Host.
func (h *Host) Jobs() plugin.Jobs { return h.JobStore }

// HTTP implements plugin.Host.
func (h *Host) HTTP() *http.Client { return h.Client }

// Secret implements plugin.Host.
func (h *Host) Secret(name string) (string, error) {
	if v, ok := h.Secrets[name]; ok {
		return v, nil
	}
	return "", plugin.ErrNotFound
}

// Notify implements plugin.Host and records the notification.
func (h *Host) Notify(_ context.Context, n plugin.Notification) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.notes = append(h.notes, n)
	return nil
}

// Notifications returns what the plugin sent.
func (h *Host) Notifications() []plugin.Notification {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]plugin.Notification(nil), h.notes...)
}

// Deps returns registry dependencies backed by fakes. Plugin storage and jobs
// are shared per plugin ID across calls.
func Deps() plugin.Deps {
	var mu sync.Mutex
	kvs := map[string]*MemKV{}
	jobs := map[string]*MemJobs{}
	return plugin.Deps{
		Log:   slog.New(slog.DiscardHandler),
		Clock: clock.NewFake(Start),
		Loc:   time.UTC,
		KV: func(id string) plugin.KV {
			mu.Lock()
			defer mu.Unlock()
			if kvs[id] == nil {
				kvs[id] = NewMemKV()
			}
			return kvs[id]
		},
		Jobs: func(id string) plugin.Jobs {
			mu.Lock()
			defer mu.Unlock()
			if jobs[id] == nil {
				jobs[id] = NewMemJobs()
			}
			return jobs[id]
		},
		Notifier: func(context.Context, plugin.Notification) error { return nil },
		Secret:   func(string) (string, bool) { return "", false },
		HTTPOptions: func(allow []string) plugin.HTTPOptions {
			return plugin.HTTPOptions{AllowHosts: allow, AllowPrivate: true}
		},
	}
}

// RunContract checks the rules every plugin must follow: a valid manifest that
// agrees with the code, well-formed tools, and a clean Init and Close.
func RunContract(t *testing.T, f plugin.Factory, cfg map[string]any) {
	t.Helper()
	ctx := context.Background()

	p := f()
	m := p.Manifest()
	if err := m.Validate(); err != nil {
		t.Fatalf("manifest invalid: %v", err)
	}

	reg := plugin.NewRegistry(Deps())
	if err := reg.Load(ctx, m.ID, f, cfg); err != nil {
		t.Fatalf("plugin failed to load: %v", err)
	}
	if got := len(reg.Tools()); got != len(m.Tools) {
		t.Errorf("registry has %d tools, manifest declares %d", got, len(m.Tools))
	}
	for _, ref := range reg.Tools() {
		if err := ref.Tool.Validate(); err != nil {
			t.Error(err)
		}
		if strings.Contains(ref.Wire(), ".") || len(ref.Wire()) > 64 {
			t.Errorf("wire name %q is not a valid function name", ref.Wire())
		}
	}
	if err := reg.Close(ctx); err != nil {
		t.Errorf("close: %v", err)
	}
}

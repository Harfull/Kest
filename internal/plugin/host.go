package plugin

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/Harfull/Kest/internal/clock"
)

// Host is what a plugin may use. It is deliberately small, and every handle is
// already scoped to the plugin that receives it.
type Host interface {
	// Logger is scoped with the plugin ID.
	Logger() *slog.Logger
	// Clock is the injected clock; use it instead of time.Now.
	Clock() clock.Clock
	// Location is the owner's time zone, for turning "9am" into an instant.
	Location() *time.Location
	// Config is this plugin's validated configuration section.
	Config() Config
	// Secret returns a secret the manifest declared, or an error.
	Secret(name string) (string, error)
	// KV is plugin-scoped key/value storage.
	KV() KV
	// Jobs schedules work for this plugin. Requires the plugin to implement JobProvider.
	Jobs() Jobs
	// HTTP is a client that enforces the manifest's network allowlist and
	// refuses private and loopback addresses.
	HTTP() *http.Client
	// Notify sends the owner a message outside of a conversation.
	Notify(ctx context.Context, n Notification) error
}

// Notification is an unsolicited message to the owner.
type Notification struct {
	Text string
	// Urgent bypasses quiet-hour silencing. Use only for things the owner asked
	// to be told about at a specific time, such as a reminder.
	Urgent bool
}

// KV is a small persistent key/value store scoped to one plugin.
type KV interface {
	Get(ctx context.Context, key string) (string, error) // ErrNotFound if absent
	Set(ctx context.Context, key, value string) error
	Delete(ctx context.Context, key string) error
	// List returns key/value pairs whose key starts with prefix, ordered by key.
	List(ctx context.Context, prefix string) ([]KVItem, error)
}

// KVItem is one stored entry.
type KVItem struct {
	Key   string
	Value string
}

// Jobs schedules work for a plugin. Jobs are persistent.
type Jobs interface {
	Schedule(ctx context.Context, spec JobSpec) (id string, err error)
	Cancel(ctx context.Context, id string) error // ErrNotFound if absent
	// List returns this plugin's scheduled jobs in run order.
	List(ctx context.Context) ([]JobInfo, error)
}

// JobSpec describes a job to schedule.
type JobSpec struct {
	Name    string    // one of the plugin's JobNames
	Payload string    // opaque string handed back to RunJob
	Rule    string    // recurrence rule such as "daily@07:30"; empty for one-off
	At      time.Time // first run (required for one-off jobs)
	// Misfire is "run_once" (default), "skip" or "expire_after:<duration>".
	Misfire string
}

// JobInfo describes a scheduled job.
type JobInfo struct {
	ID      string
	Name    string
	Payload string
	Rule    string
	NextRun time.Time
}

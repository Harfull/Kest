package scheduler

import (
	"fmt"
	"strings"
	"time"
)

// State is a job's lifecycle state.
type State string

// Job states.
const (
	StateScheduled State = "scheduled"
	StateRunning   State = "running"
	StateDone      State = "done"
	StatePaused    State = "paused"
	StateFailed    State = "failed"
)

// Misfire says what to do with a job that is found overdue, for example after
// the process was down. It never replays more than one missed run.
type Misfire struct {
	kind  string        // run_once, skip, expire_after
	grace time.Duration // for expire_after
}

// Predefined misfire policies.
var (
	// MisfireRunOnce runs a late job once, however late. Use for reminders the
	// owner asked for.
	MisfireRunOnce = Misfire{kind: "run_once"}
	// MisfireSkip drops a late run and waits for the next one.
	MisfireSkip = Misfire{kind: "skip"}
)

// MisfireExpireAfter runs a late job only if it is no more than d late. Use for
// coaching nudges, where a stale "good morning" is worse than none.
func MisfireExpireAfter(d time.Duration) Misfire {
	return Misfire{kind: "expire_after", grace: d}
}

// ParseMisfire parses "run_once", "skip" or "expire_after:30m".
func ParseMisfire(s string) (Misfire, error) {
	switch {
	case s == "" || s == "run_once":
		return MisfireRunOnce, nil
	case s == "skip":
		return MisfireSkip, nil
	case strings.HasPrefix(s, "expire_after:"):
		d, err := time.ParseDuration(strings.TrimPrefix(s, "expire_after:"))
		if err != nil || d <= 0 {
			return Misfire{}, fmt.Errorf("misfire %q: want a positive duration like expire_after:30m", s)
		}
		return MisfireExpireAfter(d), nil
	}
	return Misfire{}, fmt.Errorf("unknown misfire policy %q", s)
}

// String returns the stored form of the policy.
func (m Misfire) String() string {
	if m.kind == "expire_after" {
		return "expire_after:" + m.grace.String()
	}
	if m.kind == "" {
		return "run_once"
	}
	return m.kind
}

// shouldRun reports whether a job that is lateBy overdue should still run.
func (m Misfire) shouldRun(lateBy time.Duration) bool {
	switch m.kind {
	case "skip":
		return lateBy <= onTimeSlack
	case "expire_after":
		return lateBy <= m.grace
	default:
		return true
	}
}

// onTimeSlack is how late a job may be and still count as on time, covering
// scheduler wake-up latency.
const onTimeSlack = 30 * time.Second

// Spec describes a job to create.
type Spec struct {
	Owner   string // system, coach, or plugin:<id>
	Name    string // handler name registered with Register
	Payload string // JSON passed to the handler; defaults to {}
	Rule    string // recurrence rule; empty for a one-off job
	Zone    string // IANA zone for wall-clock rules; defaults to UTC
	// At is the first run. Required for one-off jobs; for recurring jobs it is
	// optional and defaults to the first time the rule produces after now.
	At      time.Time
	Misfire Misfire
}

// Job is a stored job.
type Job struct {
	ID        string
	Owner     string
	Name      string
	Payload   string
	Rule      string
	Zone      string
	NextRunAt time.Time
	Misfire   Misfire
	State     State
	LastRunAt time.Time
	RunCount  int
	LastError string
	CreatedAt time.Time
}

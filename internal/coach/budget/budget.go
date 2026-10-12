// Package budget decides whether Kest may send a proactive message right now.
//
// More messages are not better: tailored prompts help a little, and people
// habituate to fixed daily ones. So there is a daily cap, a minimum gap, no
// messages in quiet hours, and an automatic back-off when messages go
// unanswered. See docs/research/behavior-science/README.md (design rule 6).
//
// The policy is a pure function of the message history, so it can be tested
// over simulated months.
package budget

import (
	"fmt"
	"strings"
	"time"

	"github.com/Harfull/Kest/internal/store"
)

// Priority says how much a message matters.
type Priority int

// Priorities.
const (
	// Low messages are the first to be dropped: nudges and questions about preferences.
	Low Priority = iota
	// Normal messages are the daily rhythm: morning, habit prompts, evening, weekly.
	Normal
)

// Policy holds the limits. Zero values fall back to the defaults in New.
type Policy struct {
	// MaxPerDay caps proactive messages per local day.
	MaxPerDay int
	// MinGap is the shortest time between two proactive messages.
	MinGap time.Duration
	// ResponseWindow is how long the owner has to react before a message counts
	// as unanswered.
	ResponseWindow time.Duration
	// Loc defines the local day.
	Loc *time.Location
}

// New returns a Policy with defaults filled in: 3 a day, 90 minutes apart,
// 3 hours to respond.
func New(maxPerDay int, loc *time.Location) Policy {
	return Policy{MaxPerDay: maxPerDay, MinGap: 90 * time.Minute, ResponseWindow: 3 * time.Hour, Loc: loc}
}

// Decision is the policy's answer.
type Decision struct {
	OK     bool
	Reason string // why not, for logs
}

func yes() Decision { return Decision{OK: true} }

func no(format string, args ...any) Decision { return Decision{Reason: fmt.Sprintf(format, args...)} }

// kindOf strips a suffix such as "habit:abc123" down to "habit".
func kindOf(kind string) string {
	k, _, _ := strings.Cut(kind, ":")
	return k
}

// Ignored counts the most recent messages that went unanswered, newest first,
// stopping at the first one the owner reacted to. Messages still inside the
// response window are undecided and skipped over.
func (p Policy) Ignored(history []store.Notification, now time.Time) int {
	n := 0
	for i := len(history) - 1; i >= 0; i-- {
		m := history[i]
		if !m.RespondedAt.IsZero() {
			break
		}
		if now.Sub(m.TS) < p.ResponseWindow {
			continue
		}
		n++
	}
	return n
}

// limits returns the effective daily cap and minimum gap after back-off.
//
//	0-1 ignored: as configured
//	2:           one fewer a day, gaps doubled
//	3-5:         one a day, at least 12 hours apart
//	6 or more:   one every three days
func (p Policy) limits(ignored int) (cap int, gap time.Duration) {
	cap, gap = p.MaxPerDay, p.MinGap
	switch {
	case ignored >= 6:
		return 1, 72 * time.Hour
	case ignored >= 3:
		return 1, 12 * time.Hour
	case ignored == 2:
		return max(1, cap-1), gap * 2
	}
	return cap, gap
}

// Allow decides whether a message of the given kind may go out at now.
// history holds the messages sent in at least the last week, oldest first.
// inQuiet says whether now falls in quiet hours.
func (p Policy) Allow(now time.Time, kind string, prio Priority, history []store.Notification, inQuiet bool) Decision {
	if inQuiet {
		return no("quiet hours")
	}
	if p.MaxPerDay <= 0 {
		return no("proactive messages are turned off")
	}

	ignored := p.Ignored(history, now)
	cap, gap := p.limits(ignored)

	// Low-priority messages give way first when the owner has gone quiet.
	if prio == Low && ignored >= 3 && kindOf(kind) != "backoff" {
		return no("backing off after %d unanswered messages", ignored)
	}

	loc := p.Loc
	if loc == nil {
		loc = time.UTC
	}
	today := now.In(loc).Format("2006-01-02")
	sentToday := 0
	for _, m := range history {
		if m.TS.In(loc).Format("2006-01-02") == today {
			sentToday++
		}
	}
	if sentToday >= cap {
		return no("daily limit of %d reached", cap)
	}

	if len(history) > 0 {
		last := history[len(history)-1]
		if since := now.Sub(last.TS); since < gap {
			return no("last message was %s ago, minimum gap is %s", since.Round(time.Minute), gap)
		}
	}
	return yes()
}

// ShouldAskToBackOff reports whether it is time to ask the owner whether to
// message less: three or more unanswered messages, and no such question in the
// last week.
func (p Policy) ShouldAskToBackOff(history []store.Notification, now time.Time) bool {
	if p.Ignored(history, now) < 3 {
		return false
	}
	for _, m := range history {
		if kindOf(m.Kind) == "backoff" && now.Sub(m.TS) < 7*24*time.Hour {
			return false
		}
	}
	return true
}

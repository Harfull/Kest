package mailkit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Harfull/Kest/internal/plugin"
)

// Nudge job and storage names.
const (
	// JobCheckInbox is the scheduler job name both email plugins register.
	JobCheckInbox = "check_inbox"
	kvSeen        = "nudge/seen"
	maxSeen       = 300
	// lookback bounds how old an unread message may be and still be announced.
	lookback = 24 * time.Hour
	// maxPerCheck bounds nudges per check, so a burst of mail is one summary.
	maxPerCheck = 3
)

// Rules choose which unread mail is worth a nudge. With no rules nothing is.
type Rules struct {
	// From matches the sender: an address, or "@domain.com" for a whole domain.
	From []string
	// Words match the subject, case-insensitively.
	Words []string
}

// ParseList splits a comma-separated setting into trimmed, non-empty items.
func ParseList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.ToLower(strings.TrimSpace(p)); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (r Rules) any() bool { return len(r.From) > 0 || len(r.Words) > 0 }

func (r Rules) matches(s Summary) bool {
	from, subject := strings.ToLower(s.From), strings.ToLower(s.Subject)
	for _, f := range r.From {
		if strings.HasPrefix(f, "@") {
			if strings.Contains(from, f+">") || strings.HasSuffix(from, f) {
				return true
			}
		} else if strings.Contains(from, f) {
			return true
		}
	}
	for _, w := range r.Words {
		if strings.Contains(subject, w) {
			return true
		}
	}
	return false
}

// Nudger announces new mail that matches the owner's rules. It is opt-in: a
// plugin only builds one when the owner turned nudges on.
type Nudger struct {
	Host    plugin.Host
	Backend Backend
	Rules   Rules
}

// Check looks for unread, matching, not yet announced mail and tells the owner
// about it. The notification goes through the coach's budget, so quiet hours
// and the daily cap apply; mail held back is announced on a later check, within
// a day, instead of being forgotten.
func (n *Nudger) Check(ctx context.Context) error {
	if !n.Rules.any() {
		return nil
	}
	found, err := n.Backend.Search(ctx, Query{UnreadOnly: true, SinceDays: 2, Limit: 20})
	if err != nil {
		return fmt.Errorf("check inbox: %w", err)
	}
	seen := n.loadSeen(ctx)
	have := map[string]bool{}
	for _, id := range seen {
		have[id] = true
	}
	cutoff := n.Host.Clock().Now().Add(-lookback)

	var fresh []Summary
	for _, s := range found {
		if have[s.ID] || (!s.Date.IsZero() && s.Date.Before(cutoff)) || !n.Rules.matches(s) {
			continue
		}
		fresh = append(fresh, s)
	}
	if len(fresh) == 0 {
		return nil
	}

	sent := 0
	for _, s := range fresh {
		if sent >= maxPerCheck {
			break
		}
		err := n.Host.Notify(ctx, plugin.Notification{Text: nudgeText(s)})
		if errors.Is(err, plugin.ErrNotSent) {
			break // quiet hours or over budget: try again on a later check
		}
		if err != nil {
			return fmt.Errorf("send nudge: %w", err)
		}
		seen = append(seen, s.ID)
		sent++
	}
	if sent == maxPerCheck && len(fresh) > sent {
		// Whatever is left is covered by the first nudges; do not repeat it as more.
		for _, s := range fresh[sent:] {
			seen = append(seen, s.ID)
		}
	}
	return n.saveSeen(ctx, seen)
}

func nudgeText(s Summary) string {
	from := oneLine(s.From)
	if i := strings.Index(from, " <"); i > 0 {
		from = from[:i] // the name is enough in a chat message
	}
	return fmt.Sprintf("New email from %s: %s", clip(from, 60), clip(oneLine(s.Subject), 100))
}

func (n *Nudger) loadSeen(ctx context.Context) []string {
	raw, err := n.Host.KV().Get(ctx, kvSeen)
	if err != nil {
		return nil
	}
	var ids []string
	if json.Unmarshal([]byte(raw), &ids) != nil {
		return nil
	}
	return ids
}

func (n *Nudger) saveSeen(ctx context.Context, ids []string) error {
	if len(ids) > maxSeen {
		ids = ids[len(ids)-maxSeen:]
	}
	raw, _ := json.Marshal(ids)
	return n.Host.KV().Set(ctx, kvSeen, string(raw))
}

// EnsureJob makes the check_inbox job match the owner's setting: scheduled
// every `minutes` when enabled, absent when not. It is safe to call on every
// start.
func EnsureJob(ctx context.Context, h plugin.Host, enabled bool, minutes int) error {
	jobs, err := h.Jobs().List(ctx)
	if err != nil {
		return fmt.Errorf("list jobs: %w", err)
	}
	rule := fmt.Sprintf("every:%dm", minutes)
	have := false
	for _, j := range jobs {
		if j.Name != JobCheckInbox {
			continue
		}
		if enabled && j.Rule == rule && !have {
			have = true
			continue
		}
		if err := h.Jobs().Cancel(ctx, j.ID); err != nil && !errors.Is(err, plugin.ErrNotFound) {
			return fmt.Errorf("cancel job: %w", err)
		}
	}
	if !enabled || have {
		return nil
	}
	_, err = h.Jobs().Schedule(ctx, plugin.JobSpec{
		Name: JobCheckInbox, Rule: rule, At: h.Clock().Now().Add(time.Minute), Misfire: "skip",
	})
	if err != nil {
		return fmt.Errorf("schedule inbox check: %w", err)
	}
	return nil
}

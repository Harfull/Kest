package app

import (
	"context"
	"strings"

	"github.com/Harfull/Kest/internal/audit"
)

// auditLines is how many entries /audit shows.
const auditLines = 10

// auditList shows the owner what Kest has done on their behalf, newest first.
func (c *chat) auditList(ctx context.Context, chatID int64) {
	entries, err := c.audit.Recent(ctx, auditLines)
	if err != nil {
		c.log.ErrorContext(ctx, "read audit log", "err", err)
		c.say(ctx, chatID, msgGenericFail)
		return
	}
	if len(entries) == 0 {
		c.say(ctx, chatID, msgAuditEmpty)
		return
	}
	lines := make([]string, len(entries))
	for i, e := range entries {
		lines[i] = audit.Format(e, c.loc)
	}
	c.say(ctx, chatID, "Lately:\n"+strings.Join(lines, "\n"))
}

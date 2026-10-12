package app

import (
	"context"
	"strings"

	"github.com/Harfull/Kest/internal/telegram"
)

// browserProgress tells the owner a browsing step has started. The agent calls
// it for the first few browser tools of a turn only, so a long task shows life
// without filling the chat.
func (c *chat) browserProgress(ctx context.Context, tool string) {
	if c.messageSet == nil || c.agent.Locked() {
		return
	}
	kind := "browser_work"
	switch strings.TrimPrefix(tool, "browser.") {
	case "navigate_page", "navigate_history":
		kind = "browser_open"
	case "screenshot":
		kind = "browser_capture"
	case "read_page", "scroll_page", "find_text", "list_tabs", "wait_for":
		kind = "browser_read"
	}
	text, _, err := c.messageSet.Render(kind, nil, "", 0)
	if err != nil {
		c.log.ErrorContext(ctx, "render browser progress", "err", err)
		return
	}
	if _, err := c.tr.Send(ctx, telegram.Reply{ChatID: c.ownerChat, Text: text, Silent: true}); err != nil {
		c.log.WarnContext(ctx, "send browser progress", "err", err)
	}
}

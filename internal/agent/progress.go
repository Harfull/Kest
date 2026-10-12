package agent

import (
	"context"
	"strings"
)

type progressKey struct{}

// turnProgress is owned by the serial tool loop, so callbacks cannot race.
type turnProgress struct {
	remaining int
	report    func(context.Context, string)
}

func withToolProgress(ctx context.Context, report func(context.Context, string)) context.Context {
	// Four steps show useful progress without filling the chat during a long turn.
	return context.WithValue(ctx, progressKey{}, &turnProgress{remaining: 4, report: report})
}

func reportToolProgress(ctx context.Context, tool string) {
	p, _ := ctx.Value(progressKey{}).(*turnProgress)
	if p == nil || p.report == nil || p.remaining == 0 || !strings.HasPrefix(tool, "browser.") {
		return
	}
	p.remaining--
	p.report(ctx, tool)
}

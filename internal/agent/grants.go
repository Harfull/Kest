package agent

import (
	"context"
	"encoding/json"

	"github.com/Harfull/Kest/internal/plugin"
)

type grantsKey struct{}

// turnGrants records which plugins the owner approved for the rest of the
// turn. Like the rest of the loop state it is owned by the serial tool loop.
type turnGrants struct {
	plugins map[string]bool
}

func grantPlugin(ctx context.Context, pluginID string) {
	if g, _ := ctx.Value(grantsKey{}).(*turnGrants); g != nil {
		g.plugins[pluginID] = true
	}
}

// grantCovers reports whether an owner's earlier "Allow for this task" press
// already covers this call. Only tools that opted in with Grantable can be
// covered, and the tool may narrow the grant for a specific call.
func grantCovers(ctx context.Context, ref plugin.ToolRef, args json.RawMessage) bool {
	if !ref.Tool.Grantable {
		return false
	}
	g, _ := ctx.Value(grantsKey{}).(*turnGrants)
	if g == nil || !g.plugins[ref.Plugin] {
		return false
	}
	return ref.Tool.GrantCovers == nil || ref.Tool.GrantCovers(ctx, args)
}

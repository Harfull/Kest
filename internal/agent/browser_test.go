package agent

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/Harfull/Kest/internal/llm"
	"github.com/Harfull/Kest/internal/plugin"
)

func TestBrowserToolsRemainVisibleOnFollowUps(t *testing.T) {
	refs := append(bigToolbox(), ref("browser", "navigate_page", "Navigate a page."), ref("browser", "send_screenshot", "Send an image."))
	for _, message := range []string{"try now", "yes", "again", "what happened?", "continue"} {
		t.Run(message, func(t *testing.T) {
			got := wires(routeTools(refs, message))
			for _, want := range []string{"browser__navigate_page", "browser__send_screenshot"} {
				if !slices.Contains(got, want) {
					t.Fatalf("follow-up hid browser tool: %v", got)
				}
			}
		})
	}
}

func TestPromptUsesLiveCapabilitiesAfterIncorrectHistory(t *testing.T) {
	tool := ref("browser", "navigate_page", "Open a page.")
	tool.Tool.Schema = json.RawMessage(`{"type":"object"}`)
	tool.Tool.Handler = func(context.Context, plugin.Call) (plugin.Result, error) { return plugin.Result{}, nil }
	chat := &scripted{steps: []step{func(req llm.ChatRequest) (*llm.ChatResponse, error) {
		if !strings.Contains(req.Messages[0].Content, "- browser: navigate_page") || !strings.Contains(req.Messages[0].Content, "overrides older capability claims") {
			t.Fatal("live capabilities missing from prompt")
		}
		return say("I can try the browser now.")(req)
	}}}
	a, _ := newAgent(t, chat, box{refs: []plugin.ToolRef{tool}}, nil, nil)
	if _, err := a.Reply(context.Background(), "try now"); err != nil {
		t.Fatal(err)
	}
}

func TestProgressWaitsForApprovalAndIsBounded(t *testing.T) {
	for _, approve := range []bool{false, true} {
		t.Run(map[bool]string{false: "declined", true: "approved"}[approve], func(t *testing.T) {
			tool := ref("browser", "navigate_page", "Navigate.")
			tool.Tool.Risk, tool.Tool.Outbound = plugin.RiskWrite, true
			tool.Tool.Handler = func(context.Context, plugin.Call) (plugin.Result, error) {
				return plugin.Result{Content: "page opened"}, nil
			}
			tool.Tool.Schema = json.RawMessage(`{"type":"object"}`)
			var progress []string
			cf := &answerer{approve: approve}
			a, _ := newAgent(t, &scripted{}, box{refs: []plugin.ToolRef{tool}}, cf, func(o *Options) {
				o.Progress = func(_ context.Context, tool string) { progress = append(progress, tool) }
			})
			ctx := withToolProgress(context.Background(), a.progress)
			for range 6 {
				a.runTool(ctx, tc("call", "browser__navigate_page", `{}`))
			}
			want := 0
			if approve {
				want = 4
			}
			if len(progress) != want {
				t.Fatalf("progress count = %d, want %d", len(progress), want)
			}
		})
	}
}

func TestScreenshotPixelsReachModelAsUntrustedTurnInput(t *testing.T) {
	tool := ref("browser", "inspect_screenshot", "Inspect a screenshot.")
	tool.Tool.Outbound = true
	tool.Tool.Schema = json.RawMessage(`{"type":"object"}`)
	tool.Tool.Handler = func(context.Context, plugin.Call) (plugin.Result, error) {
		return plugin.Result{Content: "captured", Image: &plugin.Image{MediaType: "image/png", Data: []byte("fixture")}}, nil
	}
	chat := &scripted{steps: []step{callTools(tc("shot", "browser__inspect_screenshot", `{}`)), func(req llm.ChatRequest) (*llm.ChatResponse, error) {
		last := req.Messages[len(req.Messages)-1]
		if len(last.Images) != 1 || last.Role != llm.RoleUser || !strings.Contains(last.Content, "trust=\"untrusted\"") {
			t.Fatal("model did not receive untrusted image input")
		}
		return say("I can see the screenshot.")(req)
	}}}
	a, db := newAgent(t, chat, box{refs: []plugin.ToolRef{tool}}, &answerer{approve: true}, nil)
	if _, err := a.Reply(context.Background(), "inspect the screenshot"); err != nil {
		t.Fatal(err)
	}
	stored, err := db.RecentMessages(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range stored {
		if strings.Contains(message.Content, "data:image") {
			t.Fatal("image pixels persisted in history")
		}
	}
}

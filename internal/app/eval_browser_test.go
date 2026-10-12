//go:build eval

package app

// Live browsing evaluation. It runs the whole app with a real model, a real
// Chromium and a fake Telegram, visits a public website, and costs a few cents.
// It is never part of a normal test run:
//
//	KEST_OPENROUTER_API_KEY=sk-or-... KEST_BROWSER_MODULE_PATH=$PWD/deploy/browser/node_modules/playwright \
//	  go test -tags eval -run EvalBrowsing -v ./internal/app
//
// See docs/guides/model-evaluation.md.

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Harfull/Kest/internal/agent"
	"github.com/Harfull/Kest/internal/config"
	"github.com/Harfull/Kest/internal/telegram"
)

// approveAsTask presses "Allow for this task" on every approval prompt, as a
// hurried owner would, and counts the prompts.
func approveAsTask(e *env, prompts *int) (stop func()) {
	done := make(chan struct{})
	go func() {
		seen := map[int]bool{}
		for {
			select {
			case <-done:
				return
			case <-time.After(100 * time.Millisecond):
			}
			for i, r := range e.fake.Snapshot() {
				if seen[i] || len(r.Buttons) == 0 {
					continue
				}
				seen[i] = true
				*prompts++
				data := r.Buttons[0][0].Data
				if len(r.Buttons) > 1 {
					data = r.Buttons[1][0].Data // Allow for this task
				}
				e.send(telegram.Update{Callback: &telegram.Callback{ID: "eval", Data: data, MessageID: i + 1}})
			}
		}
	}()
	return func() { close(done) }
}

func TestEvalBrowsing(t *testing.T) {
	key := os.Getenv("KEST_OPENROUTER_API_KEY")
	if key == "" || os.Getenv("KEST_BROWSER_MODULE_PATH") == "" {
		t.Skip("set KEST_OPENROUTER_API_KEY and KEST_BROWSER_MODULE_PATH to run the browsing evaluation")
	}
	e := startWith(t, func(c *config.Config) {
		c.LLM.BaseURL = config.Defaults().LLM.BaseURL
		c.LLM.APIKey = config.Secret(key)
		if m := os.Getenv("KEST_MODEL_GENERAL"); m != "" {
			c.LLM.Models.General = m
		}
		c.EnabledPlugins = []string{"reminders", "tasks", "webfetch", "browser"}
		c.Plugins = map[string]map[string]any{"browser": {"module_path": os.Getenv("KEST_BROWSER_MODULE_PATH")}}
	})
	prompts := 0
	defer approveAsTask(e, &prompts)()

	ask := func(update telegram.Update, wantPhotos int) string {
		t.Helper()
		before := len(e.fake.Snapshot())
		e.send(update)
		deadline := time.Now().Add(150 * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(300 * time.Millisecond)
			s := e.fake.Snapshot()
			if len(s) > before {
				last := s[len(s)-1]
				if len(last.Buttons) == 0 && !last.Silent && len(e.fake.Photos()) >= wantPhotos {
					return last.Text
				}
			}
		}
		for i, r := range e.fake.Snapshot() {
			t.Logf("message %d (buttons %d, silent %v): %s", i+1, len(r.Buttons), r.Silent, r.Text)
		}
		t.Logf("photos sent: %d", len(e.fake.Photos()))
		t.Logf("log tail:\n%s", tail(e.log.String(), 3000))
		t.Fatalf("no answer to %q", update.Text)
		return ""
	}

	// No mention of the word "browser": Kest should know it can open the page.
	reply := ask(telegram.Update{MessageID: 1, Text: "what does the page at example.com say? send me a screenshot of it too"}, 1)
	t.Logf("reply 1: %s", reply)
	if strings.Contains(strings.ToLower(reply), "can't") && strings.Contains(strings.ToLower(reply), "browser") {
		t.Errorf("Kest claimed it cannot browse: %s", reply)
	}
	if !strings.Contains(strings.ToLower(reply), "example domain") {
		t.Errorf("the answer does not report the page title: %s", reply)
	}
	if photos := e.fake.Photos(); len(photos) < 1 {
		t.Error("no screenshot reached Telegram although one was asked for")
	}

	// A follow-up that never names the browser, in a reply to the screenshot.
	reply = ask(telegram.Update{MessageID: 2, Text: "scroll down and tell me what the link at the bottom says",
		ReplyTo: &telegram.Quoted{MessageID: 9, FromBot: true, Text: "Here is the page."}}, 1)
	t.Logf("reply 2: %s", reply)
	if strings.Contains(strings.ToLower(reply), "don't have") || strings.Contains(strings.ToLower(reply), "cannot browse") {
		t.Errorf("Kest said it could not continue browsing: %s", reply)
	}

	t.Logf("approval prompts: %d", prompts)
	if prompts > 2 {
		t.Errorf("the owner was asked %d times for two small tasks", prompts)
	}
	_ = agent.Once // keep the import meaningful if the evaluation grows
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

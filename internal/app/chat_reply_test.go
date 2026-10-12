package app

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/jpeg"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Harfull/Kest/internal/plugin"
	"github.com/Harfull/Kest/internal/telegram"
)

// sitePlugin offers one grantable, outbound tool, like the browser's.
type sitePlugin struct{ runs *atomic.Int32 }

var siteManifest = plugin.MustParseManifest(`id = "site"
name = "Site"
version = "1"
description = "A grantable tool for tests."
[[tools]]
name = "visit"
risk = "write"`)

func (s sitePlugin) Manifest() plugin.Manifest               { return siteManifest }
func (s sitePlugin) Init(context.Context, plugin.Host) error { return nil }
func (s sitePlugin) Close(context.Context) error             { return nil }
func (s sitePlugin) Tools() []plugin.Tool {
	return []plugin.Tool{{
		Name: "visit", Description: "Visit a page.", Risk: plugin.RiskWrite, Outbound: true, Grantable: true,
		Schema: json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"}},"additionalProperties":false}`),
		Handler: func(context.Context, plugin.Call) (plugin.Result, error) {
			s.runs.Add(1)
			return plugin.Result{Content: "page", Untrusted: true}, nil
		},
	}}
}

func withSite(runs *atomic.Int32) Option {
	return WithPlugins(map[string]plugin.Factory{"site": func() plugin.Plugin { return sitePlugin{runs} }})
}

// visitTwice scripts the model to visit two pages, then answer.
func visitTwice(req chatRequest) string {
	results := 0
	for _, m := range req.Messages {
		if m.Role == "tool" {
			results++
		}
	}
	switch results {
	case 0:
		return toolCallMessage("c1", "site__visit", `{"url":"https://example.com/a"}`)
	case 1:
		return toolCallMessage("c2", "site__visit", `{"url":"https://example.com/b"}`)
	}
	return textMessage("Visited both.")
}

func TestAllowForThisTaskApprovesTheWholeTaskOnce(t *testing.T) {
	var runs atomic.Int32
	e := startWith(t, enable("site"), withSite(&runs))
	e.router.script.Store(visitTwice)

	e.send(telegram.Update{Text: "look at both pages"})

	prompt := e.lastText(t, 1)
	if !strings.Contains(prompt, "site.visit") || !strings.Contains(prompt, "Allow for this task") {
		t.Fatalf("prompt = %q", prompt)
	}
	r := e.fake.Snapshot()[0]
	if len(r.Buttons) != 2 || len(r.Buttons[0]) != 2 || len(r.Buttons[1]) != 1 || r.Buttons[1][0].Label != "Allow for this task" {
		t.Fatalf("buttons = %+v", r.Buttons)
	}

	e.send(telegram.Update{Callback: &telegram.Callback{ID: "cb", Data: r.Buttons[1][0].Data, MessageID: 1}})

	if got := e.lastText(t, 2); got != "Visited both." {
		t.Fatalf("final = %q", got)
	}
	if runs.Load() != 2 {
		t.Fatalf("tool ran %d times, want 2", runs.Load())
	}
	prompts := 0
	for _, m := range e.fake.Snapshot() {
		if len(m.Buttons) > 0 {
			prompts++
		}
	}
	if prompts != 1 {
		t.Fatalf("the owner was asked %d times, want once", prompts)
	}
	if acks := e.fake.Acks(); len(acks) == 0 || acks[len(acks)-1] != "Allowed for this task" {
		t.Fatalf("acks = %v", acks)
	}
}

func TestGoAheadOnlyApprovesOneStep(t *testing.T) {
	var runs atomic.Int32
	e := startWith(t, enable("site"), withSite(&runs))
	e.router.script.Store(visitTwice)

	e.send(telegram.Update{Text: "look at both pages"})
	e.lastText(t, 1)
	e.send(telegram.Update{Callback: &telegram.Callback{ID: "cb1", Data: e.fake.Snapshot()[0].Buttons[0][0].Data, MessageID: 1}})

	waitFor(t, "a second prompt", func() bool {
		n := 0
		for _, m := range e.fake.Snapshot() {
			if len(m.Buttons) > 0 {
				n++
			}
		}
		return n == 2
	})
	if runs.Load() != 1 {
		t.Fatalf("tool ran %d times before the second approval", runs.Load())
	}
}

func TestRepliesAreThreadedOnlyWhenTheyHelp(t *testing.T) {
	e := start(t)

	e.send(telegram.Update{MessageID: 10, Text: "plain question"})
	e.lastText(t, 1)
	e.send(telegram.Update{MessageID: 11, Text: "a follow-up", ReplyTo: &telegram.Quoted{MessageID: 4, FromBot: true, Text: "Shall I book the 9am slot?"}})

	e.lastText(t, 2)
	got := e.fake.Snapshot()
	if got[0].ReplyTo != 0 {
		t.Errorf("a quick answer was threaded under %d", got[0].ReplyTo)
	}
	if got[1].ReplyTo != 11 {
		t.Errorf("a reply to a specific message was threaded under %d, want 11", got[1].ReplyTo)
	}
}

func TestSlowAnswersAreThreadedUnderTheQuestion(t *testing.T) {
	e := start(t)
	e.router.script.Store(func(chatRequest) string {
		e.clk.Advance(slowReply + 1)
		return textMessage("That took a while.")
	})
	e.send(telegram.Update{MessageID: 20, Text: "something slow"})
	e.lastText(t, 1)
	if got := e.fake.Snapshot()[0].ReplyTo; got != 20 {
		t.Fatalf("a slow answer was threaded under %d, want 20", got)
	}
}

func TestTheMessageBeingRepliedToReachesTheModel(t *testing.T) {
	e := start(t)
	e.send(telegram.Update{MessageID: 30, Text: "yes that one", ReplyTo: &telegram.Quoted{MessageID: 4, FromBot: true, Text: "Shall I book the 9am slot?"}})
	e.lastText(t, 1)
	body, _ := e.router.lastChat.Load().(string)
	for _, want := range []string{"In reply to your earlier message", "Shall I book the 9am slot?", "yes that one"} {
		if !strings.Contains(body, want) {
			t.Errorf("model request lacks %q", want)
		}
	}
}

func jpegBytes(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, 4, 4)), nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestPhotosAndScreenshotRepliesAreShownToTheModel(t *testing.T) {
	e := start(t)
	e.fake.Files["photo"] = jpegBytes(t)
	e.fake.Files["shot"] = jpegBytes(t)

	e.send(telegram.Update{MessageID: 40, Text: "is this right?", Photo: &telegram.Photo{FileID: "photo"}})
	e.lastText(t, 1)
	body, _ := e.router.lastChat.Load().(string)
	if !strings.Contains(body, "data:image/jpeg;base64,") || !strings.Contains(body, "is this right?") {
		t.Errorf("the photo did not reach the model: %.300s", body)
	}

	// Replying to a screenshot Kest sent shows the model that screenshot.
	e.send(telegram.Update{MessageID: 41, Text: "zoom in on the price", ReplyTo: &telegram.Quoted{MessageID: 9, FromBot: true, Text: "The cart", Photo: &telegram.Photo{FileID: "shot"}}})
	e.lastText(t, 2)
	body, _ = e.router.lastChat.Load().(string)
	if !strings.Contains(body, "data:image/jpeg;base64,") || !strings.Contains(body, "The cart") {
		t.Errorf("the replied-to screenshot did not reach the model: %.300s", body)
	}

	// A photo that cannot be fetched does not stop the conversation, and the
	// model is told so rather than left to guess.
	e.send(telegram.Update{MessageID: 42, Text: "and this?", Photo: &telegram.Photo{FileID: "gone"}})
	e.lastText(t, 3)
	body, _ = e.router.lastChat.Load().(string)
	if !strings.Contains(body, "could not be loaded") {
		t.Errorf("model was not told the photo failed: %.300s", body)
	}
}

func TestPhotoWithoutACaptionStillGetsAnAnswer(t *testing.T) {
	e := start(t)
	e.fake.Files["photo"] = jpegBytes(t)
	e.send(telegram.Update{MessageID: 50, Photo: &telegram.Photo{FileID: "photo"}})
	e.lastText(t, 1)
	body, _ := e.router.lastChat.Load().(string)
	if !strings.Contains(body, "the owner sent a photo") {
		t.Errorf("a bare photo had no text for the model: %.300s", body)
	}
}

func TestTypingIndicatorShowsWhileTheTurnRuns(t *testing.T) {
	e := start(t)
	e.send(telegram.Update{MessageID: 60, Text: "hello"})
	e.lastText(t, 1)
	if e.fake.Typings() < 1 {
		t.Fatal("no typing indicator was shown during the turn")
	}
}

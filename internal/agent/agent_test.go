package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Harfull/Kest/internal/clock"
	"github.com/Harfull/Kest/internal/llm"
	"github.com/Harfull/Kest/internal/store"
)

// scriptedChat is a Chatter that records requests and returns canned replies.
type scriptedChat struct {
	reqs  []llm.ChatRequest
	reply string
	err   error
}

func (s *scriptedChat) Chat(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	s.reqs = append(s.reqs, req)
	if s.err != nil {
		return nil, s.err
	}
	return &llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: s.reply}, Usage: llm.Usage{CompletionTokens: 7}}, nil
}

func setup(t *testing.T, chat *scriptedChat) (*Agent, *store.DB) {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "kest.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	loc, err := time.LoadLocation("UTC")
	if err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(time.Date(2026, 10, 11, 14, 3, 0, 0, time.UTC))
	return New(chat, db, clk, loc, 4), db
}

func TestPersonaMatchesDocs(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "persona", "prompt-core.md"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile("(?s)```text\r?\n(.*?)\r?\n```").FindSubmatch(doc)
	if m == nil {
		t.Fatal("no fenced text block in docs/persona/prompt-core.md")
	}
	want := strings.TrimSpace(strings.ReplaceAll(string(m[1]), "\r\n", "\n"))
	got := strings.TrimSpace(strings.ReplaceAll(persona, "\r\n", "\n"))
	if got != want {
		t.Error("internal/agent/persona.txt is out of sync with docs/persona/prompt-core.md; update both together")
	}
}

func TestReplyBuildsPromptAndStoresTurns(t *testing.T) {
	chat := &scriptedChat{reply: "  Morning. Ten minutes still counts.  "}
	a, db := setup(t, chat)
	ctx := context.Background()

	got, err := a.Reply(ctx, "I don't feel like walking")
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if got != "Morning. Ten minutes still counts." {
		t.Errorf("reply = %q", got)
	}

	msgs := chat.reqs[0].Messages
	if msgs[0].Role != llm.RoleSystem || !strings.Contains(msgs[0].Content, "You are Kest") {
		t.Errorf("system prompt missing persona: %.60q", msgs[0].Content)
	}
	if !strings.Contains(msgs[0].Content, "Sunday 11 October 2026, 14:03") {
		t.Errorf("system prompt missing local time: %q", msgs[0].Content)
	}
	if last := msgs[len(msgs)-1]; last.Role != llm.RoleUser || last.Content != "I don't feel like walking" {
		t.Errorf("last message = %+v", last)
	}

	stored, _ := db.RecentMessages(ctx, 10)
	if len(stored) != 2 || stored[0].Role != "user" || stored[1].Role != "assistant" || stored[1].Origin != "kest" {
		t.Fatalf("stored = %+v", stored)
	}
	if stored[1].Tokens != 7 {
		t.Errorf("tokens = %d, want 7", stored[1].Tokens)
	}
}

func TestHistoryIsReplayedAndCapped(t *testing.T) {
	chat := &scriptedChat{reply: "ok"}
	a, _ := setup(t, chat) // history window of 4 messages
	ctx := context.Background()

	for _, in := range []string{"one", "two", "three"} {
		if _, err := a.Reply(ctx, in); err != nil {
			t.Fatal(err)
		}
	}

	msgs := chat.reqs[2].Messages // third call: system + 4 prior + new user
	var contents []string
	for _, m := range msgs[1:] {
		contents = append(contents, m.Content)
	}
	want := []string{"one", "ok", "two", "ok", "three"}
	if strings.Join(contents, ",") != strings.Join(want, ",") {
		t.Errorf("history in prompt = %v, want %v", contents, want)
	}
}

func TestModelFailureKeepsUserMessage(t *testing.T) {
	chat := &scriptedChat{err: errors.New("down")}
	a, db := setup(t, chat)

	if _, err := a.Reply(context.Background(), "hello"); err == nil {
		t.Fatal("Reply() succeeded despite model failure")
	}
	stored, _ := db.RecentMessages(context.Background(), 10)
	if len(stored) != 1 || stored[0].Content != "hello" {
		t.Errorf("stored = %+v, want the user's message only", stored)
	}
}

func TestEmptyInputsAreRejected(t *testing.T) {
	a, _ := setup(t, &scriptedChat{reply: "  "})

	if _, err := a.Reply(context.Background(), "   "); err == nil {
		t.Error("blank message accepted")
	}
	if _, err := a.Reply(context.Background(), "hi"); err == nil {
		t.Error("blank model reply accepted")
	}
}

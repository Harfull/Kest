package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Harfull/Kest/internal/clock"
	"github.com/Harfull/Kest/internal/config"
	"github.com/Harfull/Kest/internal/scheduler"
	"github.com/Harfull/Kest/internal/store"
	"github.com/Harfull/Kest/internal/telegram"
)

// syncBuffer is a bytes.Buffer-like writer safe for a logger and a reader.
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// fakeRouter is a stand-in for OpenRouter.
type fakeRouter struct {
	chatStatus   atomic.Int32 // non-zero: chat returns this error status
	speechStatus atomic.Int32 // non-zero: speech returns this error status
	pcmOnly      atomic.Bool  // speech model refuses MP3 and returns PCM, like Gemini TTS
	transcript   atomic.Value // string
	chats        atomic.Int32
	lastChat     atomic.Value // string: the raw body of the latest chat request
	script       atomic.Value // func(chatRequest) string: the assistant message as JSON
}

// chatRequest is the part of a chat completion request the fake inspects.
type chatRequest struct {
	Messages []struct {
		Role       string `json:"role"`
		Content    string `json:"content"`
		ToolCallID string `json:"tool_call_id"`
	} `json:"messages"`
	Tools []json.RawMessage `json:"tools"`
}

// lastRole returns the content of the last message and its role.
func (r chatRequest) last() (role, content string) {
	m := r.Messages[len(r.Messages)-1]
	return m.Role, m.Content
}

// textMessage and toolCallMessage build assistant messages for a script.
func textMessage(s string) string {
	b, _ := json.Marshal(map[string]any{"role": "assistant", "content": s})
	return string(b)
}

func toolCallMessage(id, name, args string) string {
	b, _ := json.Marshal(map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{
		map[string]any{"id": id, "type": "function", "function": map[string]any{"name": name, "arguments": args}},
	}})
	return string(b)
}

func (f *fakeRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	fail := func(status int32) bool {
		if status == 0 {
			return false
		}
		w.WriteHeader(int(status))
		_, _ = io.WriteString(w, `{"error":{"code":400,"message":"nope"}}`)
		return true
	}

	switch r.URL.Path {
	case "/chat/completions":
		f.chats.Add(1)
		f.lastChat.Store(string(body))
		if fail(f.chatStatus.Load()) {
			return
		}
		var req chatRequest
		_ = json.Unmarshal(body, &req)
		_, last := req.last()
		msg := textMessage("Noted: " + last)
		if fn, ok := f.script.Load().(func(chatRequest) string); ok {
			msg = fn(req)
		}
		_, _ = fmt.Fprintf(w, `{"id":"g1","model":"m","choices":[{"finish_reason":"stop","message":%s}],`+
			`"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8,"cost":0.01}}`, msg)
	case "/audio/transcriptions":
		text, _ := f.transcript.Load().(string)
		out, _ := json.Marshal(map[string]any{"text": text, "usage": map[string]any{"cost": 0.001}})
		_, _ = w.Write(out)
	case "/audio/speech":
		if fail(f.speechStatus.Load()) {
			return
		}
		var sreq struct {
			Format string `json:"response_format"`
		}
		_ = json.Unmarshal(body, &sreq)
		if f.pcmOnly.Load() {
			if sreq.Format != "pcm" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"error":{"code":400,"message":"Gemini TTS only supports response_format=\"pcm\". Got \""+sreq.Format+"\"."}}`)
				return
			}
			w.Header().Set("Content-Type", "audio/pcm;rate=24000;channels=1")
			_, _ = w.Write(tonePCM())
			return
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("ID3-speech"))
	default:
		http.NotFound(w, r)
	}
}

type env struct {
	app    *App
	fake   *telegram.Fake
	router *fakeRouter
	clk    *clock.Fake
	log    *syncBuffer
	cancel context.CancelFunc
	done   chan error
	dir    string
}

var epoch = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

// start launches a full App against a fake OpenRouter and a fake Telegram.
func start(t *testing.T) *env { return startWith(t, nil) }

// startWith is start with a hook to adjust the config and extra options.
func startWith(t *testing.T, mutate func(*config.Config), opts ...Option) *env {
	t.Helper()

	router := &fakeRouter{}
	router.transcript.Store("I skipped my run")
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	dir := filepath.Join(t.TempDir(), "data")
	cfg := config.Defaults()
	cfg.DataDir = dir
	cfg.Telegram.Token = "123:token"
	cfg.Telegram.OwnerIDs = []int64{1}
	cfg.LLM.APIKey = "key"
	cfg.LLM.BaseURL = srv.URL
	if mutate != nil {
		mutate(&cfg)
	}

	e := &env{
		fake:   telegram.NewFake(),
		router: router,
		clk:    clock.NewFake(epoch),
		log:    &syncBuffer{},
		dir:    dir,
		done:   make(chan error, 1),
	}
	log := slog.New(slog.NewTextHandler(e.log, nil))

	all := append([]Option{WithTransport(e.fake), WithClock(e.clk)}, opts...)
	a, err := New(cfg, log, all...)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	e.app = a

	ctx, cancel := context.WithCancel(context.Background())
	e.cancel = cancel
	go func() { e.done <- a.Run(ctx) }()
	t.Cleanup(func() { e.stop(t) })

	waitFor(t, "ready", func() bool { return strings.Contains(e.log.String(), ReadyMessage) })
	return e
}

func (e *env) stop(t *testing.T) {
	t.Helper()
	e.cancel()
	select {
	case err := <-e.done:
		if err != nil {
			t.Errorf("Run() error = %v", err)
		}
		e.done <- nil // allow a second stop
	case <-time.After(ShutdownTimeout + time.Second):
		t.Error("Run() did not return after cancel")
	}
}

func (e *env) send(u telegram.Update) {
	u.ChatID, u.UserID = 1, 1
	e.fake.Inject(context.Background(), u)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// lastText waits for n replies and returns the text of the last one.
func (e *env) lastText(t *testing.T, n int) string {
	t.Helper()
	waitFor(t, fmt.Sprintf("%d replies", n), func() bool { return len(e.fake.Snapshot()) >= n })
	r := e.fake.Snapshot()
	return r[len(r)-1].Text
}

func TestStartupRegistersMenuAndReportsReady(t *testing.T) {
	e := start(t)

	if got := e.fake.Commands(); len(got) != len(commandMenu) {
		t.Errorf("commands registered = %v", got)
	}
	if !strings.Contains(e.log.String(), "kest: ready") {
		t.Error("ready message missing; the Pterodactyl egg depends on it")
	}
}

func TestTextMessageGetsReply(t *testing.T) {
	e := start(t)

	e.send(telegram.Update{Text: "hello there"})

	if got := e.lastText(t, 1); got != "Noted: hello there" {
		t.Errorf("reply = %q", got)
	}
}

func TestConversationKeepsOrder(t *testing.T) {
	e := start(t)

	for _, m := range []string{"one", "two", "three"} {
		e.send(telegram.Update{Text: m})
	}
	e.lastText(t, 3)

	var got []string
	for _, r := range e.fake.Snapshot() {
		got = append(got, r.Text)
	}
	if strings.Join(got, "|") != "Noted: one|Noted: two|Noted: three" {
		t.Errorf("replies out of order: %v", got)
	}
}

func TestVoiceNoteGetsVoiceReply(t *testing.T) {
	e := start(t)
	e.fake.Files["f1"] = []byte("OggS")

	e.send(telegram.Update{Voice: &telegram.Voice{FileID: "f1", Duration: 3, MIMEType: "audio/ogg", Size: 4}})

	waitFor(t, "voice reply", func() bool { return len(e.fake.Voices()) == 1 })
	if v := e.fake.Voices()[0]; string(v.Audio) != "ID3-speech" || v.Ext != "mp3" {
		t.Errorf("voice = %q as %s, want MP3 passed through untouched", v.Audio, v.Ext)
	}
	if n := len(e.fake.Snapshot()); n != 0 {
		t.Errorf("%d text replies sent alongside the voice reply", n)
	}
}

func TestPCMOnlySpeechModelStillGetsAVoiceReply(t *testing.T) {
	// Gemini TTS refuses MP3 and returns raw PCM: Kest must adapt by itself.
	e := start(t)
	e.router.pcmOnly.Store(true)
	e.fake.Files["f1"] = []byte("OggS")

	e.send(telegram.Update{Voice: &telegram.Voice{FileID: "f1", MIMEType: "audio/ogg", Size: 4}})

	waitFor(t, "voice reply", func() bool { return len(e.fake.Voices()) == 1 })
	v := e.fake.Voices()[0]
	if v.Ext != "ogg" || !bytes.HasPrefix(v.Audio, []byte("OggS")) || !bytes.Contains(v.Audio[:100], []byte("OpusHead")) {
		t.Errorf("voice = %s, %d bytes; want an Ogg/Opus file Telegram can play", v.Ext, len(v.Audio))
	}
	if n := len(e.fake.Snapshot()); n != 0 {
		t.Errorf("%d text replies sent although the voice reply worked", n)
	}
	if !strings.Contains(e.log.String(), "switching") {
		t.Errorf("the format switch should be logged:\n%s", e.log.String())
	}

	// A second voice note goes straight to PCM.
	e.send(telegram.Update{Voice: &telegram.Voice{FileID: "f1", MIMEType: "audio/ogg", Size: 4}})
	waitFor(t, "second voice reply", func() bool { return len(e.fake.Voices()) == 2 })
	if got := strings.Count(e.log.String(), "text-to-speech model rejected a format"); got != 1 {
		t.Errorf("format switch logged %d times, want once (the choice is remembered)", got)
	}
}

func TestVoiceReplyFallsBackToText(t *testing.T) {
	e := start(t)
	e.fake.Files["f1"] = []byte("OggS")
	e.router.speechStatus.Store(400)

	e.send(telegram.Update{Voice: &telegram.Voice{FileID: "f1", MIMEType: "audio/ogg", Size: 4}})

	if got := e.lastText(t, 1); got != "Noted: I skipped my run" {
		t.Errorf("fallback reply = %q", got)
	}
	if len(e.fake.Voices()) != 0 {
		t.Error("voice sent despite synthesis failure")
	}
}

func TestUnintelligibleVoiceNote(t *testing.T) {
	e := start(t)
	e.fake.Files["f1"] = []byte("OggS")
	e.router.transcript.Store("   ")

	e.send(telegram.Update{Voice: &telegram.Voice{FileID: "f1", MIMEType: "audio/ogg", Size: 4}})

	if got := e.lastText(t, 1); got != msgNoVoice {
		t.Errorf("reply = %q", got)
	}
	if e.router.chats.Load() != 0 {
		t.Error("model called for an empty transcript")
	}
}

func TestOversizeVoiceNoteIsRefused(t *testing.T) {
	e := start(t)

	e.send(telegram.Update{Voice: &telegram.Voice{FileID: "big", Size: maxVoiceDownload + 1}})

	if got := e.lastText(t, 1); got != msgVoiceTooBig {
		t.Errorf("reply = %q", got)
	}
}

func TestModelFailureGetsCalmMessage(t *testing.T) {
	e := start(t)
	e.router.chatStatus.Store(400)

	e.send(telegram.Update{Text: "hi"})

	got := e.lastText(t, 1)
	if got != msgModelDown {
		t.Errorf("reply = %q", got)
	}
	if strings.Contains(got, "nope") || strings.Contains(strings.ToLower(got), "error") {
		t.Errorf("upstream error text leaked to the owner: %q", got)
	}
}

func TestCommands(t *testing.T) {
	e := start(t)

	tests := []struct {
		cmd  string
		want string
	}{
		{"start", "I'm Kest"},
		{"help", "/forget"},
		{"nonsense", msgUnknownCommand},
	}
	for i, tt := range tests {
		e.send(telegram.Update{Command: tt.cmd})
		if got := e.lastText(t, i+1); !strings.Contains(got, tt.want) {
			t.Errorf("/%s reply = %q, want it to contain %q", tt.cmd, got, tt.want)
		}
	}
}

func TestStatusShowsSpendAndSchedule(t *testing.T) {
	e := start(t)
	e.send(telegram.Update{Text: "hi"}) // costs $0.01 via the fake router
	e.lastText(t, 1)

	e.send(telegram.Update{Command: "status"})
	got := e.lastText(t, 2)

	for _, want := range []string{"Spend today: $0.01 of $1.00", "maintenance", "03:30"} {
		if !strings.Contains(got, want) {
			t.Errorf("status missing %q:\n%s", want, got)
		}
	}
}

func TestExportSendsData(t *testing.T) {
	e := start(t)
	e.send(telegram.Update{Text: "remember the stairs"})
	e.lastText(t, 1)

	e.send(telegram.Update{Command: "export"})
	waitFor(t, "export", func() bool { return len(e.fake.Documents()) == 1 })

	doc := e.fake.Documents()[0]
	if doc.Filename != "kest-export-2026-10-11.json" {
		t.Errorf("filename = %q", doc.Filename)
	}
	if !json.Valid(doc.Data) || !strings.Contains(string(doc.Data), "remember the stairs") {
		t.Errorf("export content wrong: %s", doc.Data)
	}
}

// forgetButtons asks for /forget and returns the button data for yes and no.
func (e *env) forgetButtons(t *testing.T, n int) (yes, no string) {
	t.Helper()
	e.send(telegram.Update{Command: "forget"})
	e.lastText(t, n)
	r := e.fake.Snapshot()[n-1]
	if len(r.Buttons) != 1 || len(r.Buttons[0]) != 2 {
		t.Fatalf("forget confirmation has wrong buttons: %+v", r.Buttons)
	}
	return r.Buttons[0][0].Data, r.Buttons[0][1].Data
}

func (e *env) exportHasMessages(t *testing.T, afterDocs int) bool {
	t.Helper()
	e.send(telegram.Update{Command: "export"})
	waitFor(t, "export", func() bool { return len(e.fake.Documents()) > afterDocs })
	docs := e.fake.Documents()
	var out struct {
		Messages []any `json:"messages"`
	}
	if err := json.Unmarshal(docs[len(docs)-1].Data, &out); err != nil {
		t.Fatal(err)
	}
	return len(out.Messages) > 0
}

func TestForgetDeletesAfterConfirmation(t *testing.T) {
	e := start(t)
	e.send(telegram.Update{Text: "private thought"})
	e.lastText(t, 1)

	yes, _ := e.forgetButtons(t, 2)
	if !e.exportHasMessages(t, 0) {
		t.Fatal("test setup: expected messages before forgetting")
	}

	e.send(telegram.Update{Callback: &telegram.Callback{ID: "cb", Data: yes, MessageID: 2}})
	if got := e.lastText(t, 3); got != msgForgetDone {
		t.Errorf("reply = %q", got)
	}
	if cleared := e.fake.Cleared(); len(cleared) != 1 || cleared[0] != 2 {
		t.Errorf("buttons not cleared: %v", cleared)
	}
	if e.exportHasMessages(t, 1) {
		t.Error("messages survived /forget")
	}
}

func TestForgetCancelKeepsData(t *testing.T) {
	e := start(t)
	e.send(telegram.Update{Text: "keep me"})
	e.lastText(t, 1)

	_, no := e.forgetButtons(t, 2)
	e.send(telegram.Update{Callback: &telegram.Callback{ID: "cb", Data: no, MessageID: 2}})

	if got := e.lastText(t, 3); got != msgForgetCancel {
		t.Errorf("reply = %q", got)
	}
	if !e.exportHasMessages(t, 0) {
		t.Error("messages deleted despite cancel")
	}
}

func TestForgetConfirmationExpires(t *testing.T) {
	e := start(t)
	e.send(telegram.Update{Text: "keep me"})
	e.lastText(t, 1)

	yes, _ := e.forgetButtons(t, 2)
	e.clk.Advance(confirmTTL + time.Minute)
	e.send(telegram.Update{Callback: &telegram.Callback{ID: "cb", Data: yes, MessageID: 2}})

	if got := e.lastText(t, 3); got != msgForgetExpired {
		t.Errorf("reply = %q", got)
	}
	if !e.exportHasMessages(t, 0) {
		t.Error("expired confirmation still deleted data")
	}
}

func TestUnknownCallbacksAreIgnored(t *testing.T) {
	e := start(t)

	for _, data := range []string{"", "other:thing", "forget:maybe:1", "forget:yes:notanumber", "forget:yes"} {
		e.send(telegram.Update{Callback: &telegram.Callback{ID: "cb", Data: data, MessageID: 9}})
	}
	// A normal message afterwards proves the bot is alive and nothing was sent.
	e.send(telegram.Update{Text: "still here"})
	if got := e.lastText(t, 1); got != "Noted: still here" {
		t.Errorf("reply = %q", got)
	}
	if n := len(e.fake.Snapshot()); n != 1 {
		t.Errorf("%d replies, want exactly 1", n)
	}
}

func TestParseForget(t *testing.T) {
	tests := []struct {
		in     string
		choice string
		stamp  int64
		ok     bool
	}{
		{"forget:yes:100", "yes", 100, true},
		{"forget:no:5", "no", 5, true},
		{"forget:yes", "", 0, false},
		{"forget:maybe:1", "", 0, false},
		{"forget:yes:x", "", 0, false},
		{"other:yes:1", "", 0, false},
	}
	for _, tt := range tests {
		choice, stamp, ok := parseForget(tt.in)
		if choice != tt.choice || stamp != tt.stamp || ok != tt.ok {
			t.Errorf("parseForget(%q) = %q, %d, %v", tt.in, choice, stamp, ok)
		}
	}
}

func TestVoiceFormat(t *testing.T) {
	for in, want := range map[string]string{
		"audio/ogg": "ogg", "": "ogg", "audio/mpeg": "mp3", "audio/mp4": "m4a", "AUDIO/WAV": "wav",
	} {
		if got := voiceFormat(in); got != want {
			t.Errorf("voiceFormat(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMaintenanceBacksUpAndPrunes(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, "kest.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close() //nolint:errcheck // test cleanup

	clk := clock.NewFake(epoch)
	old := epoch.Add(-messageRetention - time.Hour)
	for _, ts := range []time.Time{old, epoch} {
		if _, err := db.AddMessage(ctx, store.Message{TS: ts, Role: "user", Content: "x"}); err != nil {
			t.Fatal(err)
		}
	}

	// Pre-existing backups: more than we keep, plus an unrelated file.
	backups := filepath.Join(dir, "backups")
	if err := os.MkdirAll(backups, 0o750); err != nil {
		t.Fatal(err)
	}
	for d := 1; d <= 9; d++ {
		name := fmt.Sprintf("kest-202610%02d.db", d)
		if err := os.WriteFile(filepath.Join(backups, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(backups, "notes.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	m := &maintenance{db: db, dir: dir, clock: clk, log: slog.New(slog.DiscardHandler)}
	for range 2 { // running twice on one day must replace, not fail
		if err := m.run(ctx, scheduler.Job{}); err != nil {
			t.Fatalf("run() error = %v", err)
		}
	}

	entries, _ := os.ReadDir(backups)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != backupsToKeep+1 { // 7 backups + notes.txt
		t.Errorf("files after pruning = %v", names)
	}
	if _, err := os.Stat(filepath.Join(backups, "kest-20261011.db")); err != nil {
		t.Errorf("today's backup missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(backups, "notes.txt")); err != nil {
		t.Error("unrelated file was removed")
	}

	msgs, _ := db.RecentMessages(ctx, 10)
	if len(msgs) != 1 {
		t.Errorf("messages after pruning = %d, want 1", len(msgs))
	}
}

// tonePCM is a third of a second of a 440 Hz tone: 24 kHz, 16-bit, mono.
func tonePCM() []byte {
	n := 24000 / 3
	out := make([]byte, 0, n*2)
	for i := range n {
		v := int16(9000 * math.Sin(2*math.Pi*440*float64(i)/24000))
		out = append(out, byte(uint16(v)), byte(uint16(v)>>8))
	}
	return out
}

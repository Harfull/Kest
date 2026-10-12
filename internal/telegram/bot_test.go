package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const testToken = "123456:TEST-token-abcdefghijklmnopqrstuvwxyz0123"

// fakeAPI is a minimal Telegram Bot API server.
type fakeAPI struct {
	t   *testing.T
	srv *httptest.Server

	mu        sync.Mutex
	updates   []string            // pending getUpdates results, as raw JSON
	calls     map[string][]string // method -> request bodies
	rejectFmt int                 // number of sendMessage calls to reject as unparseable
	fileSize  int64
	fileBody  []byte
}

func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	f := &fakeAPI{t: t, calls: map[string][]string{}, fileBody: []byte("OggS-voice")}
	f.fileSize = int64(len(f.fileBody))
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPI) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)

	if strings.HasPrefix(r.URL.Path, "/file/bot"+testToken+"/") {
		_, _ = w.Write(f.fileBody)
		return
	}

	method := strings.TrimPrefix(r.URL.Path, "/bot"+testToken+"/")
	if method == r.URL.Path {
		http.Error(w, "bad token", http.StatusUnauthorized)
		return
	}

	f.mu.Lock()
	f.calls[method] = append(f.calls[method], encodeForm(r.Header.Get("Content-Type"), body))
	f.mu.Unlock()

	reply := func(result string) { _, _ = fmt.Fprintf(w, `{"ok":true,"result":%s}`, result) }

	switch method {
	case "getMe":
		reply(`{"id":123456,"is_bot":true,"first_name":"Kest","username":"kest_bot"}`)
	case "getUpdates":
		f.mu.Lock()
		pending := f.updates
		f.updates = nil
		f.mu.Unlock()
		if len(pending) == 0 {
			time.Sleep(50 * time.Millisecond)
		}
		reply("[" + strings.Join(pending, ",") + "]")
	case "sendMessage":
		f.mu.Lock()
		reject := f.rejectFmt > 0
		if reject {
			f.rejectFmt--
		}
		f.mu.Unlock()
		if reject {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"ok":false,"error_code":400,"description":"Bad Request: can't parse entities: Unsupported start tag"}`)
			return
		}
		reply(`{"message_id":77,"chat":{"id":42,"type":"private"},"date":1}`)
	case "sendVoice":
		reply(`{"message_id":78,"chat":{"id":42,"type":"private"},"date":1}`)
	case "getFile":
		reply(fmt.Sprintf(`{"file_id":"f1","file_size":%d,"file_path":"voice/file_1.oga"}`, f.fileSize))
	case "sendChatAction", "answerCallbackQuery", "setMyCommands":
		reply("true")
	case "editMessageReplyMarkup":
		reply(`{"message_id":77,"chat":{"id":42,"type":"private"},"date":1}`)
	default:
		http.Error(w, "unexpected method "+method, http.StatusNotFound)
	}
}

func (f *fakeAPI) queue(updateJSON string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates = append(f.updates, updateJSON)
}

func (f *fakeAPI) bodies(method string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls[method]...)
}

func newBot(t *testing.T, f *fakeAPI) *Bot {
	t.Helper()
	b, err := New(Options{Token: testToken, OwnerIDs: []int64{42}, ServerURL: f.srv.URL})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return b
}

func privateMsg(id int64, userID int64, extra string) string {
	return fmt.Sprintf(`{"update_id":%d,"message":{"message_id":%d,"date":1,"from":{"id":%d,"is_bot":false,"first_name":"x"},
"chat":{"id":%d,"type":"private"},%s}}`, id, id, userID, userID, extra)
}

// run starts the bot and returns a channel of delivered updates.
func run(t *testing.T, b *Bot) <-chan Update {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan Update, 16)
	done := make(chan struct{})
	go func() {
		_ = b.Run(ctx, func(_ context.Context, u Update) { got <- u })
		close(done)
	}()
	t.Cleanup(func() { cancel(); <-done })
	return got
}

func next(t *testing.T, ch <-chan Update) Update {
	t.Helper()
	select {
	case u := <-ch:
		return u
	case <-time.After(3 * time.Second):
		t.Fatal("no update delivered")
		return Update{}
	}
}

func TestOwnerGuardDropsStrangers(t *testing.T) {
	f := newFakeAPI(t)
	b := newBot(t, f)
	got := run(t, b)

	f.queue(privateMsg(1, 999, `"text":"let me in"`)) // not an owner
	f.queue(privateMsg(2, 42, `"text":"hello"`))      // owner
	f.queue(`{"update_id":3,"message":{"message_id":3,"date":1,"from":{"id":42,"is_bot":false,"first_name":"x"},` +
		`"chat":{"id":-100,"type":"supergroup"},"text":"in a group"}}`) // owner, but a group
	f.queue(privateMsg(4, 42, `"text":"second"`))

	if u := next(t, got); u.Text != "hello" || u.UserID != 42 || u.ChatID != 42 {
		t.Errorf("first delivered = %+v, want the owner's hello", u)
	}
	if u := next(t, got); u.Text != "second" {
		t.Errorf("second delivered = %+v, want 'second' (stranger and group dropped)", u)
	}
	if b.Dropped() != 2 {
		t.Errorf("Dropped() = %d, want 2", b.Dropped())
	}
}

func TestNormalizesCommandsVoiceAndCallbacks(t *testing.T) {
	f := newFakeAPI(t)
	b := newBot(t, f)
	got := run(t, b)

	f.queue(privateMsg(1, 42, `"text":"/Status@kest_bot  now please"`))
	f.queue(privateMsg(2, 42, `"voice":{"file_id":"f1","file_unique_id":"u","duration":4,"mime_type":"audio/ogg","file_size":2048}`))
	f.queue(`{"update_id":3,"callback_query":{"id":"cb1","from":{"id":42,"is_bot":false,"first_name":"x"},` +
		`"message":{"message_id":55,"date":1,"chat":{"id":42,"type":"private"}},"data":"confirm:abc"}}`)
	f.queue(`{"update_id":4,"callback_query":{"id":"cb2","from":{"id":7,"is_bot":false,"first_name":"x"},` +
		`"message":{"message_id":56,"date":1,"chat":{"id":42,"type":"private"}},"data":"confirm:evil"}}`)
	f.queue(privateMsg(5, 42, `"text":"done"`))

	cmd := next(t, got)
	if cmd.Command != "status" || cmd.Args != "now please" || cmd.Text != "" {
		t.Errorf("command = %+v", cmd)
	}

	voice := next(t, got)
	if voice.Voice == nil || voice.Voice.FileID != "f1" || voice.Voice.Duration != 4 || voice.Voice.MIMEType != "audio/ogg" {
		t.Errorf("voice = %+v", voice.Voice)
	}

	cb := next(t, got)
	if cb.Callback == nil || cb.Callback.Data != "confirm:abc" || cb.Callback.MessageID != 55 || cb.ChatID != 42 {
		t.Errorf("callback = %+v", cb)
	}

	// The stranger's button press must not surface.
	if u := next(t, got); u.Text != "done" {
		t.Errorf("after stranger callback got %+v, want 'done'", u)
	}
}

func TestSendConvertsFormattingAndAttachesButtonsToLastPart(t *testing.T) {
	f := newFakeAPI(t)
	b := newBot(t, f)

	long := strings.Repeat("word ", 1000) // ~5000 chars, needs two parts
	id, err := b.Send(context.Background(), Reply{
		ChatID:  42,
		Text:    "**Plan** <ok>\n\n" + long,
		Buttons: [][]Button{{{Label: "Do it", Data: "go:1"}}},
		Silent:  true,
		ReplyTo: 9,
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if id != 77 {
		t.Errorf("message id = %d, want 77", id)
	}

	calls := f.bodies("sendMessage")
	if len(calls) < 2 {
		t.Fatalf("sendMessage calls = %d, want >= 2 for long text", len(calls))
	}

	var first, last map[string]string
	_ = json.Unmarshal([]byte(calls[0]), &first)
	_ = json.Unmarshal([]byte(calls[len(calls)-1]), &last)

	if text := first["text"]; !strings.HasPrefix(text, "<b>Plan</b> &lt;ok&gt;") {
		t.Errorf("first part not converted: %.40q", text)
	}
	if first["parse_mode"] != "HTML" || first["disable_notification"] != "true" {
		t.Errorf("first part options: %v", first)
	}
	if _, has := first["reply_markup"]; has {
		t.Error("buttons attached to the first part")
	}
	if first["reply_parameters"] == "" {
		t.Error("reply_parameters missing on first part")
	}
	if last["reply_markup"] == "" {
		t.Error("buttons missing from the last part")
	}
	if _, has := last["reply_parameters"]; has {
		t.Error("reply_parameters repeated on the last part")
	}
}

func TestSendFallsBackToPlainWhenFormattingRejected(t *testing.T) {
	f := newFakeAPI(t)
	f.rejectFmt = 1
	b := newBot(t, f)

	if _, err := b.Send(context.Background(), Reply{ChatID: 42, Text: "hello **world**"}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	calls := f.bodies("sendMessage")
	if len(calls) != 2 {
		t.Fatalf("sendMessage calls = %d, want 2 (HTML then plain)", len(calls))
	}
	var retry map[string]string
	_ = json.Unmarshal([]byte(calls[1]), &retry)
	if _, has := retry["parse_mode"]; retry["text"] != "hello **world**" || has {
		t.Errorf("retry = %v, want the original plain text with no parse mode", retry)
	}
}

func TestSendRejectsBadInput(t *testing.T) {
	f := newFakeAPI(t)
	b := newBot(t, f)

	if _, err := b.Send(context.Background(), Reply{ChatID: 42, Text: "  "}); err == nil {
		t.Error("empty text accepted")
	}
	bad := Reply{ChatID: 42, Text: "x", Buttons: [][]Button{{{Label: "A", Data: strings.Repeat("d", 65)}}}}
	if _, err := b.Send(context.Background(), bad); err == nil {
		t.Error("oversize callback data accepted")
	}
	if n := len(f.bodies("sendMessage")); n != 0 {
		t.Errorf("%d requests sent for invalid input", n)
	}
}

func TestDownload(t *testing.T) {
	f := newFakeAPI(t)
	b := newBot(t, f)

	data, err := b.Download(context.Background(), "f1")
	if err != nil || string(data) != "OggS-voice" {
		t.Fatalf("Download() = %q, %v", data, err)
	}

	f.fileSize = maxDownloadBytes + 1
	if _, err := b.Download(context.Background(), "f1"); err == nil {
		t.Error("oversize file accepted")
	}
}

func TestVoiceTypingAndMenu(t *testing.T) {
	f := newFakeAPI(t)
	b := newBot(t, f)
	ctx := context.Background()

	if err := b.SendVoice(ctx, 42, []byte("ID3-mp3"), true); err != nil {
		t.Fatalf("SendVoice() error = %v", err)
	}
	if err := b.SendVoice(ctx, 42, nil, false); err == nil {
		t.Error("empty audio accepted")
	}
	if err := b.Typing(ctx, 42, true); err != nil {
		t.Fatal(err)
	}
	if err := b.SetCommands(ctx, []Command{{Name: "status", Description: "Health and spend"}}); err != nil {
		t.Fatal(err)
	}
	if err := b.AnswerCallback(ctx, "cb1", "Sent"); err != nil {
		t.Fatal(err)
	}
	if err := b.ClearButtons(ctx, 42, 77); err != nil {
		t.Fatal(err)
	}

	if body := f.bodies("sendVoice"); len(body) != 1 || !strings.Contains(body[0], "ID3-mp3") || !strings.Contains(body[0], "kest.mp3") {
		t.Errorf("sendVoice body = %v", body)
	}
	if body := f.bodies("sendChatAction"); len(body) != 1 || !strings.Contains(body[0], "record_voice") {
		t.Errorf("sendChatAction body = %v", body)
	}
}

func TestConnectErrorDoesNotLeakToken(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // nothing is listening any more

	_, err := New(Options{Token: testToken, OwnerIDs: []int64{1}, ServerURL: url})
	if err == nil {
		t.Fatal("New() succeeded against a dead server")
	}
	if strings.Contains(err.Error(), testToken) || strings.Contains(err.Error(), "TEST-token") {
		t.Errorf("error leaks the token: %v", err)
	}
}

func TestNewRequiresOwner(t *testing.T) {
	if _, err := New(Options{Token: testToken}); err == nil {
		t.Error("New() without owners succeeded")
	}
}

func TestParseCommand(t *testing.T) {
	tests := []struct{ in, cmd, args string }{
		{"/start", "start", ""},
		{"/Status@kest_bot", "status", ""},
		{"/remind  tomorrow at 9 ", "remind", "tomorrow at 9"},
		{"/settings tone=gentle", "settings", "tone=gentle"},
	}
	for _, tt := range tests {
		cmd, args := parseCommand(tt.in)
		if cmd != tt.cmd || args != tt.args {
			t.Errorf("parseCommand(%q) = %q, %q; want %q, %q", tt.in, cmd, args, tt.cmd, tt.args)
		}
	}
}

func TestFakeImplementsTransport(t *testing.T) {
	f := NewFake()
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan Update, 1)
	done := make(chan struct{})
	go func() { _ = f.Run(ctx, func(_ context.Context, u Update) { got <- u }); close(done) }()

	f.Inject(ctx, Update{Text: "hi"})
	if u := <-got; u.Text != "hi" {
		t.Errorf("got %+v", u)
	}
	if _, err := f.Send(ctx, Reply{Text: "yo"}); err != nil || len(f.Snapshot()) != 1 {
		t.Errorf("Send/Snapshot failed: %v", err)
	}
	cancel()
	<-done
}

// encodeForm turns a multipart request body into a JSON object of field name to
// value. File parts are stored as "<content>|<filename>".
func encodeForm(contentType string, body []byte) string {
	fields := map[string]string{}
	if _, params, err := mime.ParseMediaType(contentType); err == nil && params["boundary"] != "" {
		mr := multipart.NewReader(strings.NewReader(string(body)), params["boundary"])
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			data, _ := io.ReadAll(part)
			v := string(data)
			if part.FileName() != "" {
				v += "|" + part.FileName()
			}
			fields[part.FormName()] = v
		}
	}
	out, _ := json.Marshal(fields)
	return string(out)
}

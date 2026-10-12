package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Harfull/Kest/internal/clock"
	"github.com/Harfull/Kest/internal/store"
)

// fakeLedger is an in-memory Ledger.
type fakeLedger struct {
	mu    sync.Mutex
	items []store.Usage
}

func (f *fakeLedger) AddUsage(_ context.Context, u store.Usage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.items = append(f.items, u)
	return nil
}

func (f *fakeLedger) SpendSince(_ context.Context, since time.Time) (float64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var total float64
	for _, u := range f.items {
		if !u.TS.Before(since) {
			total += u.CostUSD
		}
	}
	return total, nil
}

type harness struct {
	srv    *httptest.Server
	client *Client
	ledger *fakeLedger
	clk    *clock.Fake
	slept  []time.Duration
}

// newHarness starts a fake OpenRouter. handler receives every request.
func newHarness(t *testing.T, handler http.HandlerFunc, mutate func(*Options)) *harness {
	t.Helper()
	h := &harness{
		ledger: &fakeLedger{},
		clk:    clock.NewFake(time.Date(2026, 10, 11, 15, 0, 0, 0, time.UTC)),
	}
	h.srv = httptest.NewServer(handler)
	t.Cleanup(h.srv.Close)

	opts := Options{
		BaseURL:       h.srv.URL,
		APIKey:        "test-key",
		General:       "test/general",
		Transcription: "test/stt",
		TTS:           "test/tts",
		TTSVoice:      "alloy",
		Ledger:        h.ledger,
		Clock:         h.clk,
		Backoff:       time.Millisecond,
		Sleep: func(_ context.Context, d time.Duration) error {
			h.slept = append(h.slept, d)
			return nil
		},
	}
	if mutate != nil {
		mutate(&opts)
	}
	c, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	h.client = c
	return h
}

func readJSON(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	return m
}

const okChat = `{"id":"gen-1","model":"test/general","choices":[{"finish_reason":"stop",
"message":{"role":"assistant","content":"hello"}}],
"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"cost":0.002}}`

func TestNewValidation(t *testing.T) {
	tests := []struct {
		name string
		opts Options
	}{
		{"no base url", Options{APIKey: "k", General: "m"}},
		{"no api key", Options{BaseURL: "http://x", General: "m"}},
		{"no model", Options{BaseURL: "http://x", APIKey: "k"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := New(tt.opts); err == nil {
				t.Fatal("New() succeeded, want error")
			}
		})
	}
}

func TestChatRequestShapeAndResponse(t *testing.T) {
	var got map[string]any
	var auth, ref string
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		got = readJSON(t, r)
		auth, ref = r.Header.Get("Authorization"), r.Header.Get("HTTP-Referer")
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = io.WriteString(w, okChat)
	}, func(o *Options) { o.Referer = "https://example.test" })

	resp, err := h.client.Chat(context.Background(), ChatRequest{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
		Tools: []Tool{{
			Name: "lookup", Description: "find a thing",
			Parameters: json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`),
		}},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if auth != "Bearer test-key" || ref != "https://example.test" {
		t.Errorf("headers: auth=%q referer=%q", auth, ref)
	}
	if got["model"] != "test/general" {
		t.Errorf("model = %v", got["model"])
	}
	if _, has := got["models"]; has {
		t.Error("models array sent without fallbacks")
	}
	tools := got["tools"].([]any)
	fn := tools[0].(map[string]any)["function"].(map[string]any)
	if tools[0].(map[string]any)["type"] != "function" || fn["name"] != "lookup" {
		t.Errorf("tool envelope wrong: %v", tools[0])
	}

	if resp.Message.Content != "hello" || resp.FinishReason != "stop" || resp.Usage.TotalTokens != 15 {
		t.Errorf("response = %+v", resp)
	}
	if len(h.ledger.items) != 1 || h.ledger.items[0].CostUSD != 0.002 || h.ledger.items[0].Slot != "general" {
		t.Errorf("usage not recorded: %+v", h.ledger.items)
	}
}

func TestChatFallbacksUseModelsArray(t *testing.T) {
	var got map[string]any
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		got = readJSON(t, r)
		_, _ = io.WriteString(w, okChat)
	}, func(o *Options) { o.Fallbacks = []string{"test/b", "test/c"}; o.ZDROnly = true })

	if _, err := h.client.Chat(context.Background(), ChatRequest{Messages: []Message{{Role: RoleUser, Content: "x"}}}); err != nil {
		t.Fatal(err)
	}

	models := got["models"].([]any)
	if len(models) != 3 || models[0] != "test/general" || models[2] != "test/c" {
		t.Errorf("models = %v", models)
	}
	if got["route"] != "fallback" {
		t.Errorf("route = %v", got["route"])
	}
	if _, has := got["model"]; has {
		t.Error("model sent alongside models")
	}
	if got["provider"].(map[string]any)["zdr"] != true {
		t.Errorf("zdr not requested: %v", got["provider"])
	}
}

func TestToolCallRoundTrip(t *testing.T) {
	call := `{"id":"gen-2","model":"m","choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":null,
"tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"x\"}"}}]}}],"usage":{}}`

	var second map[string]any
	n := 0
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		n++
		if n == 1 {
			_, _ = io.WriteString(w, call)
			return
		}
		second = readJSON(t, r)
		_, _ = io.WriteString(w, okChat)
	}, nil)

	first, err := h.client.Chat(context.Background(), ChatRequest{Messages: []Message{{Role: RoleUser, Content: "go"}}})
	if err != nil {
		t.Fatal(err)
	}
	if first.FinishReason != "tool_calls" || len(first.Message.ToolCalls) != 1 {
		t.Fatalf("tool call not parsed: %+v", first)
	}
	tc := first.Message.ToolCalls[0]
	if tc.ID != "call_1" || tc.Function.Name != "lookup" || tc.Function.Arguments != `{"q":"x"}` {
		t.Errorf("tool call = %+v", tc)
	}

	_, err = h.client.Chat(context.Background(), ChatRequest{Messages: []Message{
		{Role: RoleUser, Content: "go"},
		first.Message,
		{Role: RoleTool, ToolCallID: "call_1", Content: "result"},
	}})
	if err != nil {
		t.Fatal(err)
	}

	msgs := second["messages"].([]any)
	asst := msgs[1].(map[string]any)
	if asst["content"] != nil {
		t.Errorf("assistant content = %v, want null for tool-calling message", asst["content"])
	}
	if len(asst["tool_calls"].([]any)) != 1 {
		t.Error("assistant tool_calls missing from follow-up request")
	}
	if tool := msgs[2].(map[string]any); tool["tool_call_id"] != "call_1" || tool["role"] != "tool" {
		t.Errorf("tool message = %v", tool)
	}
}

func TestRetriesTransientErrors(t *testing.T) {
	n := 0
	h := newHarness(t, func(w http.ResponseWriter, _ *http.Request) {
		n++
		switch n {
		case 1:
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"code":429,"message":"slow down"}}`)
		case 2:
			w.WriteHeader(http.StatusBadGateway)
		default:
			_, _ = io.WriteString(w, okChat)
		}
	}, nil)

	if _, err := h.client.Chat(context.Background(), ChatRequest{Messages: []Message{{Role: RoleUser, Content: "x"}}}); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if n != 3 {
		t.Errorf("attempts = %d, want 3", n)
	}
	if len(h.slept) != 2 || h.slept[0] != 2*time.Second {
		t.Errorf("Retry-After not honored: slept %v", h.slept)
	}
}

func TestDoesNotRetryClientErrors(t *testing.T) {
	n := 0
	h := newHarness(t, func(w http.ResponseWriter, _ *http.Request) {
		n++
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"code":401,"message":"No auth credentials found"}}`)
	}, nil)

	_, err := h.client.Chat(context.Background(), ChatRequest{Messages: []Message{{Role: RoleUser, Content: "x"}}})

	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 401 || apiErr.Code != 401 {
		t.Fatalf("error = %v, want APIError 401", err)
	}
	if n != 1 {
		t.Errorf("attempts = %d, want 1", n)
	}
	if strings.Contains(err.Error(), "test-key") {
		t.Error("error leaks the API key")
	}
}

func TestGivesUpAfterMaxRetries(t *testing.T) {
	n := 0
	h := newHarness(t, func(w http.ResponseWriter, _ *http.Request) {
		n++
		w.WriteHeader(http.StatusServiceUnavailable)
	}, func(o *Options) { o.MaxRetries = 2 })

	if _, err := h.client.Chat(context.Background(), ChatRequest{Messages: []Message{{Role: RoleUser, Content: "x"}}}); err == nil {
		t.Fatal("Chat() succeeded, want error")
	}
	if n != 3 {
		t.Errorf("attempts = %d, want 3 (1 + 2 retries)", n)
	}
}

func TestErrorInsideOKResponse(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"error":{"code":502,"message":"upstream exploded"}}`)
	}, nil)

	_, err := h.client.Chat(context.Background(), ChatRequest{Messages: []Message{{Role: RoleUser, Content: "x"}}})
	if err == nil || !strings.Contains(err.Error(), "upstream exploded") {
		t.Fatalf("error = %v", err)
	}
}

func TestContextCancelStopsRetries(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	n := 0
	h := newHarness(t, func(w http.ResponseWriter, _ *http.Request) {
		n++
		cancel()
		w.WriteHeader(http.StatusBadGateway)
	}, nil)

	if _, err := h.client.Chat(ctx, ChatRequest{Messages: []Message{{Role: RoleUser, Content: "x"}}}); err == nil {
		t.Fatal("want error")
	}
	if n != 1 {
		t.Errorf("attempts = %d after cancel, want 1", n)
	}
}

func TestTranscribe(t *testing.T) {
	audio := []byte("OggS-fake-audio")
	var got map[string]any
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/audio/transcriptions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		got = readJSON(t, r)
		w.Header().Set("X-Generation-Id", "gen-stt")
		_, _ = io.WriteString(w, `{"text":"  I skipped my run  ","usage":{"input_tokens":80,"output_tokens":6,"cost":0.0005}}`)
	}, nil)

	text, err := h.client.Transcribe(context.Background(), audio, "ogg", "en")
	if err != nil {
		t.Fatalf("Transcribe() error = %v", err)
	}
	if text != "I skipped my run" {
		t.Errorf("text = %q", text)
	}

	in := got["input_audio"].(map[string]any)
	if in["format"] != "ogg" || in["data"] != base64.StdEncoding.EncodeToString(audio) {
		t.Errorf("input_audio = %v", in)
	}
	if got["model"] != "test/stt" || got["language"] != "en" {
		t.Errorf("payload = %v", got)
	}
	if u := h.ledger.items[0]; u.Slot != "transcription" || u.CostUSD != 0.0005 || u.GenerationID != "gen-stt" {
		t.Errorf("usage = %+v", u)
	}
}

func TestTranscribeRejectsBadInput(t *testing.T) {
	h := newHarness(t, func(http.ResponseWriter, *http.Request) { t.Error("request should not be sent") }, nil)

	if _, err := h.client.Transcribe(context.Background(), nil, "ogg", ""); err == nil {
		t.Error("empty audio accepted")
	}
	big := make([]byte, maxTranscribeBytes+1)
	if _, err := h.client.Transcribe(context.Background(), big, "ogg", ""); !errors.Is(err, ErrAudioTooLarge) {
		t.Errorf("oversize error = %v", err)
	}
}

func TestSpeak(t *testing.T) {
	var got map[string]any
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		got = readJSON(t, r)
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("ID3-fake-mp3"))
	}, nil)

	sp, err := h.client.Speak(context.Background(), "  Morning. Ready for your walk?  ")
	if err != nil {
		t.Fatalf("Speak() error = %v", err)
	}
	if string(sp.Data) != "ID3-fake-mp3" || sp.Format != FormatMP3 {
		t.Errorf("speech = %+v", sp)
	}
	if got["input"] != "Morning. Ready for your walk?" || got["voice"] != "alloy" ||
		got["response_format"] != "mp3" || got["model"] != "test/tts" {
		t.Errorf("payload = %v", got)
	}
	if h.ledger.items[0].Slot != "tts" {
		t.Errorf("usage = %+v", h.ledger.items)
	}
}

const geminiError = `{"error":{"code":400,"message":"Gemini TTS only supports response_format=\"pcm\". Got \"mp3\"."}}`

// pcmOnlyServer behaves like a model that refuses MP3, such as Gemini TTS.
func pcmOnlyServer(t *testing.T, contentType string, requests *[]string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body := readJSON(t, r)
		format, _ := body["response_format"].(string)
		*requests = append(*requests, format)
		if format != "pcm" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, geminiError)
			return
		}
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		_, _ = w.Write([]byte{1, 0, 2, 0, 3, 0})
	}
}

func TestSpeakSwitchesToPCMWhenTheModelRequiresIt(t *testing.T) {
	var requests []string
	h := newHarness(t, pcmOnlyServer(t, "audio/pcm", &requests), nil)

	sp, err := h.client.Speak(context.Background(), "Hello there")
	if err != nil {
		t.Fatalf("Speak() error = %v", err)
	}
	if sp.Format != FormatPCM || sp.SampleRate != 24000 || sp.Channels != 1 || len(sp.Data) != 6 {
		t.Errorf("speech = %+v; want PCM with the documented 24 kHz mono default", sp)
	}
	if strings.Join(requests, ",") != "mp3,pcm" {
		t.Errorf("requests = %v, want mp3 then pcm", requests)
	}

	// The next call goes straight to PCM: no wasted round trip.
	requests = nil
	if _, err := h.client.Speak(context.Background(), "And again"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(requests, ",") != "pcm" {
		t.Errorf("second call requests = %v, want just pcm (learned)", requests)
	}
}

func TestSpeakReadsRateAndChannelsFromContentType(t *testing.T) {
	var requests []string
	h := newHarness(t, pcmOnlyServer(t, "audio/pcm;rate=16000;channels=2", &requests), nil)

	sp, err := h.client.Speak(context.Background(), "Hello")
	if err != nil || sp.SampleRate != 16000 || sp.Channels != 2 {
		t.Errorf("speech = %+v, %v", sp, err)
	}
}

func TestSpeakRecognizesOtherFormatErrorWordings(t *testing.T) {
	// No explicit hint in the message: it mentions the format, so try the other one.
	n := 0
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		n++
		if body := readJSON(t, r); body["response_format"] == "mp3" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"Invalid response_format for this model"}}`)
			return
		}
		w.Header().Set("Content-Type", "audio/pcm")
		_, _ = w.Write([]byte{0, 0})
	}, nil)

	sp, err := h.client.Speak(context.Background(), "Hello")
	if err != nil || sp.Format != FormatPCM || n != 2 {
		t.Errorf("speech = %+v, %v after %d requests", sp, err, n)
	}
}

func TestSpeakStopsWhenNeitherFormatWorks(t *testing.T) {
	n := 0
	h := newHarness(t, func(w http.ResponseWriter, _ *http.Request) {
		n++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"unsupported response_format"}}`)
	}, nil)

	if _, err := h.client.Speak(context.Background(), "Hello"); err == nil {
		t.Fatal("Speak() succeeded")
	}
	if n != 2 {
		t.Errorf("requests = %d, want exactly one per format", n)
	}
}

func TestSpeakDoesNotRetryUnrelatedBadRequests(t *testing.T) {
	n := 0
	h := newHarness(t, func(w http.ResponseWriter, _ *http.Request) {
		n++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"input is too long"}}`)
	}, nil)

	if _, err := h.client.Speak(context.Background(), "Hello"); err == nil {
		t.Fatal("Speak() succeeded")
	}
	if n != 1 {
		t.Errorf("requests = %d, want 1", n)
	}
}

func TestSpeakExplainsAWrongVoice(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"Voice alloy is not available for this model"}}`)
	}, nil)

	_, err := h.client.Speak(context.Background(), "Hello")
	if err == nil || !strings.Contains(err.Error(), "KEST_TTS_VOICE") || !strings.Contains(err.Error(), "alloy") {
		t.Errorf("error = %v; it should point at KEST_TTS_VOICE", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Error("the original API error was lost")
	}
}

func TestSpeakFallsBackToTheDefaultVoice(t *testing.T) {
	var voices []any
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		body := readJSON(t, r)
		voices = append(voices, body["voice"])
		if body["voice"] != nil { // this model has no voice called alloy
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"Unknown voice: alloy"}}`)
			return
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("ID3"))
	}, nil)

	if _, err := h.client.Speak(context.Background(), "Hello"); err != nil {
		t.Fatalf("Speak() error = %v; the default voice should have been tried", err)
	}
	if len(voices) != 2 || voices[0] != "alloy" || voices[1] != nil {
		t.Errorf("voices sent = %v, want alloy then none", voices)
	}

	// Remembered: the next call omits the voice from the start.
	voices = nil
	if _, err := h.client.Speak(context.Background(), "Again"); err != nil {
		t.Fatal(err)
	}
	if len(voices) != 1 || voices[0] != nil {
		t.Errorf("second call voices = %v, want one request without a voice", voices)
	}
}

func TestSpeakDoesNotRememberADefaultVoiceThatAlsoFails(t *testing.T) {
	n := 0
	h := newHarness(t, func(w http.ResponseWriter, _ *http.Request) {
		n++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"voice is required"}}`)
	}, nil)

	_, err := h.client.Speak(context.Background(), "Hello")
	if err == nil || !strings.Contains(err.Error(), "KEST_TTS_VOICE") {
		t.Errorf("error = %v", err)
	}
	if n != 2 {
		t.Errorf("requests = %d, want the configured voice then none", n)
	}
	if _, remembered := h.client.ttsNoVoice.Load("test/tts"); remembered {
		t.Error("a failed attempt without a voice was remembered")
	}
}

func TestSpeakTrustsTheContentTypeOverTheRequest(t *testing.T) {
	// Asked for MP3, but the provider answers in PCM anyway.
	h := newHarness(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "audio/pcm;rate=24000")
		_, _ = w.Write([]byte{9, 9})
	}, nil)
	sp, err := h.client.Speak(context.Background(), "Hello")
	if err != nil || sp.Format != FormatPCM {
		t.Errorf("speech = %+v, %v", sp, err)
	}

	h = newHarness(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write([]byte("RIFF"))
	}, nil)
	if _, err := h.client.Speak(context.Background(), "Hello"); err == nil {
		t.Error("an unsupported content type was accepted")
	}
}

func TestAlternativeFormat(t *testing.T) {
	tests := []struct {
		msg   string
		tried map[string]bool
		want  string
	}{
		{`Gemini TTS only supports response_format="pcm". Got "mp3".`, map[string]bool{"mp3": true}, "pcm"},
		{`Model only supports response_format=mp3`, map[string]bool{"pcm": true}, "mp3"},
		{`supports format: pcm`, map[string]bool{"mp3": true}, "pcm"},
		{`Gemini TTS only supports response_format="pcm".`, map[string]bool{"mp3": true, "pcm": true}, ""},
		{"invalid response_format", map[string]bool{"mp3": true}, "pcm"},
		{"invalid response_format", map[string]bool{"pcm": true}, "mp3"},
		{"invalid response_format", map[string]bool{"mp3": true, "pcm": true}, ""},
		{"input is too long", map[string]bool{"mp3": true}, ""},
		{"", map[string]bool{}, ""},
	}
	for _, tt := range tests {
		if got := alternativeFormat(tt.msg, tt.tried); got != tt.want {
			t.Errorf("alternativeFormat(%q, %v) = %q, want %q", tt.msg, tt.tried, got, tt.want)
		}
	}
}

func TestSpeakRejectsBadInput(t *testing.T) {
	h := newHarness(t, func(http.ResponseWriter, *http.Request) { t.Error("request should not be sent") }, nil)

	if _, err := h.client.Speak(context.Background(), "   "); err == nil {
		t.Error("blank text accepted")
	}
	if _, err := h.client.Speak(context.Background(), strings.Repeat("a", maxSpeechChars+1)); !errors.Is(err, ErrTextTooLong) {
		t.Errorf("long text error = %v", err)
	}
}

func TestBudgetBlocksOnlyProactiveCalls(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, okChat) }, nil)

	loc, err := time.LoadLocation("UTC")
	if err != nil {
		t.Fatal(err)
	}
	b := NewBudget(1.00, h.ledger, h.clk, loc)
	h.client.SetBudget(b)

	// Spend recorded earlier today, past the limit.
	_ = h.ledger.AddUsage(context.Background(), store.Usage{TS: h.clk.Now().Add(-time.Hour), CostUSD: 1.5})

	msgs := []Message{{Role: RoleUser, Content: "x"}}

	if _, err := h.client.Chat(context.Background(), ChatRequest{Messages: msgs, Proactive: true}); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("proactive error = %v, want ErrBudgetExceeded", err)
	}
	if _, err := h.client.Chat(context.Background(), ChatRequest{Messages: msgs}); err != nil {
		t.Fatalf("user-initiated call blocked: %v", err)
	}

	// Yesterday's spend does not count.
	h.clk.Advance(24 * time.Hour)
	if err := b.Check(context.Background()); err != nil {
		t.Errorf("Check() after midnight = %v", err)
	}
}

func TestBudgetDisabledAtZero(t *testing.T) {
	ledger := &fakeLedger{}
	_ = ledger.AddUsage(context.Background(), store.Usage{TS: time.Now(), CostUSD: 100})
	b := NewBudget(0, ledger, clock.Real{}, time.UTC)
	if err := b.Check(context.Background()); err != nil {
		t.Errorf("Check() with zero limit = %v", err)
	}
}

func TestMessageJSONRoundTrip(t *testing.T) {
	in := Message{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "c", Type: "function", Function: FunctionCall{Name: "n", Arguments: "{}"}}}}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"content":null`) {
		t.Errorf("want null content: %s", b)
	}
	var out Message
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.Content != "" || len(out.ToolCalls) != 1 {
		t.Errorf("round trip = %+v", out)
	}
}

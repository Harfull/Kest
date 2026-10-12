// Package llm is Kest's client for OpenRouter: chat completions with tool
// calling, speech-to-text, text-to-speech, retries, model fallbacks and cost
// accounting.
//
// It speaks HTTP and JSON only. It knows nothing about Telegram, plugins or
// coaching; the agent package decides what to ask and what to do with answers.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Harfull/Kest/internal/clock"
	"github.com/Harfull/Kest/internal/store"
)

// maxResponseBytes bounds how much of a response body is read into memory.
// Speech responses are the largest; 32 MiB is far beyond a spoken reply.
const maxResponseBytes = 32 << 20

// Ledger records and sums model spend. *store.DB implements it.
type Ledger interface {
	AddUsage(ctx context.Context, u store.Usage) error
	SpendSince(ctx context.Context, since time.Time) (float64, error)
}

// Options configures a Client.
type Options struct {
	BaseURL string // for example https://openrouter.ai/api/v1
	APIKey  string

	// Model slots. Fallbacks apply to the General slot only.
	General       string
	Fallbacks     []string
	Transcription string
	TTS           string
	TTSVoice      string

	// ZDROnly asks OpenRouter to route only to zero-data-retention providers.
	ZDROnly bool

	// Optional attribution headers shown on openrouter.ai.
	Referer string
	Title   string

	HTTP       *http.Client  // defaults to a client with a 90 second timeout
	Ledger     Ledger        // optional; usage is not recorded when nil
	Clock      clock.Clock   // defaults to the system clock
	Log        *slog.Logger  // optional; used for notable decisions such as format switches
	MaxRetries int           // retries after the first attempt; defaults to 3
	Backoff    time.Duration // base delay; defaults to 500ms
	Sleep      func(ctx context.Context, d time.Duration) error
}

// Client talks to OpenRouter. It is safe for concurrent use.
type Client struct {
	o      Options
	budget *Budget

	// ttsFormat remembers, per speech model, the format it accepted.
	ttsFormat sync.Map
	// ttsNoVoice marks speech models that rejected the configured voice but
	// worked with the provider's default.
	ttsNoVoice sync.Map
}

// New validates opts, fills defaults and returns a Client.
func New(opts Options) (*Client, error) {
	if strings.TrimSpace(opts.BaseURL) == "" {
		return nil, errors.New("llm: base URL is required")
	}
	if opts.APIKey == "" {
		return nil, errors.New("llm: API key is required")
	}
	if opts.General == "" {
		return nil, errors.New("llm: general model is required")
	}
	opts.BaseURL = strings.TrimRight(opts.BaseURL, "/")
	if opts.HTTP == nil {
		opts.HTTP = &http.Client{Timeout: 90 * time.Second}
	}
	if opts.Clock == nil {
		opts.Clock = clock.Real{}
	}
	if opts.MaxRetries == 0 {
		opts.MaxRetries = 3
	}
	if opts.Backoff == 0 {
		opts.Backoff = 500 * time.Millisecond
	}
	if opts.Sleep == nil {
		opts.Sleep = sleepCtx
	}
	return &Client{o: opts}, nil
}

// APIError is an error response from OpenRouter or an upstream provider.
type APIError struct {
	Status  int    // HTTP status
	Code    int    // OpenRouter error code, if present
	Message string // human-readable message
}

func (e *APIError) Error() string {
	return fmt.Sprintf("openrouter: status %d: %s", e.Status, e.Message)
}

// Retryable reports whether trying the same request again might succeed.
func (e *APIError) Retryable() bool {
	return e.Status == http.StatusTooManyRequests || e.Status >= 500 || e.Status == 529
}

// do sends a JSON POST and returns the body, retrying transient failures with
// jittered exponential backoff. The request body is rebuilt for every attempt.
func (c *Client) do(ctx context.Context, path string, payload any) (body []byte, header http.Header, err error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, fmt.Errorf("encode request: %w", err)
	}

	for attempt := 0; ; attempt++ {
		body, header, err = c.once(ctx, path, data)
		if err == nil {
			return body, header, nil
		}

		var apiErr *APIError
		retryable := errors.As(err, &apiErr) && apiErr.Retryable()
		if !retryable && !isTransport(err) {
			return nil, nil, err
		}
		if attempt >= c.o.MaxRetries || ctx.Err() != nil {
			return nil, nil, err
		}

		delay := c.backoff(attempt)
		if apiErr != nil {
			if ra := retryAfter(header); ra > 0 {
				delay = ra
			}
		}
		if err := c.o.Sleep(ctx, delay); err != nil {
			return nil, nil, err
		}
	}
}

func (c *Client) once(ctx context.Context, path string, data []byte) ([]byte, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.o.BaseURL+path, bytes.NewReader(data))
	if err != nil {
		return nil, nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.o.APIKey)
	req.Header.Set("Content-Type", "application/json")
	if c.o.Referer != "" {
		req.Header.Set("HTTP-Referer", c.o.Referer)
	}
	if c.o.Title != "" {
		req.Header.Set("X-Title", c.o.Title)
	}

	resp, err := c.o.HTTP.Do(req)
	if err != nil {
		return nil, nil, &transportError{err}
	}
	defer resp.Body.Close() //nolint:errcheck // body fully read below

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, nil, &transportError{err}
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, resp.Header, parseAPIError(resp.StatusCode, body)
	}
	return body, resp.Header, nil
}

// transportError marks network-level failures, which are worth retrying.
type transportError struct{ err error }

func (e *transportError) Error() string { return "openrouter: request failed: " + e.err.Error() }
func (e *transportError) Unwrap() error { return e.err }

func isTransport(err error) bool {
	var t *transportError
	return errors.As(err, &t) && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

// parseAPIError extracts OpenRouter's {"error": {...}} envelope when present.
func parseAPIError(status int, body []byte) *APIError {
	var env struct {
		Error struct {
			Code    any    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	e := &APIError{Status: status, Message: strings.TrimSpace(string(truncate(body, 300)))}
	if json.Unmarshal(body, &env) == nil && env.Error.Message != "" {
		e.Message = env.Error.Message
		switch v := env.Error.Code.(type) {
		case float64:
			e.Code = int(v)
		case string:
			e.Code, _ = strconv.Atoi(v)
		}
	}
	return e
}

func truncate(b []byte, n int) []byte {
	if len(b) > n {
		return b[:n]
	}
	return b
}

// backoff returns base * 2^attempt with up to 50% random jitter.
func (c *Client) backoff(attempt int) time.Duration {
	d := c.o.Backoff << attempt
	return d/2 + time.Duration(rand.Int64N(int64(d/2)+1)) //nolint:gosec // jitter, not security
}

// retryAfter parses a Retry-After header given in seconds.
func retryAfter(h http.Header) time.Duration {
	if h == nil {
		return 0
	}
	secs, err := strconv.Atoi(h.Get("Retry-After"))
	if err != nil || secs <= 0 {
		return 0
	}
	return min(time.Duration(secs)*time.Second, 30*time.Second)
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// record stores usage, logging nothing: a failed ledger write must never fail
// the user-facing call that already succeeded.
func (c *Client) record(ctx context.Context, slot, model string, u Usage, genID string) {
	if c.o.Ledger == nil {
		return
	}
	_ = c.o.Ledger.AddUsage(ctx, store.Usage{
		TS:               c.o.Clock.Now(),
		Slot:             slot,
		Model:            model,
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		CostUSD:          u.Cost,
		GenerationID:     genID,
	})
}

// Logf logs an informational message if a logger is configured.
func (o Options) Logf(ctx context.Context, msg string, args ...any) {
	if o.Log != nil {
		o.Log.InfoContext(ctx, msg, args...)
	}
}

package notion

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	apiBase = "https://api.notion.com/v1"
	// minGap keeps requests under Notion's average of three a second per
	// connection (docs/research/integrations/notion.md), with a little to spare.
	minGap = 400 * time.Millisecond
	// maxRetryWait bounds how long a Retry-After is honored in one request.
	maxRetryWait = 20 * time.Second
	maxResponse  = 4 << 20
)

// client is a small Notion API client: bearer auth, a pinned version header, a
// shared request limiter and Retry-After handling.
type client struct {
	http    *http.Client
	version string
	token   func() (string, error)
	now     func() time.Time
	// sleep waits for d or until ctx ends. Tests replace it so they do not wait.
	sleep func(ctx context.Context, d time.Duration) error

	mu   sync.Mutex
	next time.Time
}

func realSleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// pace blocks until the next request may go out.
func (c *client) pace(ctx context.Context) error {
	c.mu.Lock()
	now := c.now()
	wait := time.Duration(0)
	if c.next.After(now) {
		wait = c.next.Sub(now)
	}
	c.next = now.Add(wait + minGap)
	c.mu.Unlock()
	if wait <= 0 {
		return nil
	}
	return c.sleep(ctx, wait)
}

// do sends one API request and decodes the JSON reply into out.
func (c *client) do(ctx context.Context, method, path string, query url.Values, in, out any) error {
	token, err := c.token()
	if err != nil {
		return err
	}
	var payload []byte
	if in != nil {
		if payload, err = json.Marshal(in); err != nil {
			return fmt.Errorf("encode the request: %w", err)
		}
	}
	u := apiBase + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	for attempt := 0; ; attempt++ {
		if err := c.pace(ctx); err != nil {
			return err
		}
		var body io.Reader
		if payload != nil {
			body = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, u, body)
		if err != nil {
			return errors.New("could not build the request")
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Notion-Version", c.version)
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return fmt.Errorf("reach Notion: %s", reason(err))
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
		_ = resp.Body.Close()
		if readErr != nil || len(data) > maxResponse {
			return errors.New("the Notion response was unreadable or too large")
		}
		switch {
		case resp.StatusCode >= 200 && resp.StatusCode <= 299:
			if out == nil {
				return nil
			}
			if err := json.Unmarshal(data, out); err != nil {
				return errors.New("the Notion response was not what was expected")
			}
			return nil
		case (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == 529) && attempt < 2:
			wait := time.Second
			if secs, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && secs > 0 {
				wait = time.Duration(secs) * time.Second
			}
			if wait > maxRetryWait {
				return errors.New("requests to Notion are being rate limited; try again in a minute")
			}
			if err := c.sleep(ctx, wait); err != nil {
				return err
			}
			continue
		}
		return apiError(resp.StatusCode, data)
	}
}

func reason(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err.Error() // without the URL
	}
	return err.Error()
}

// apiError turns a Notion error into a message the owner can act on.
func apiError(status int, data []byte) error {
	var e struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(data, &e)
	switch {
	case status == http.StatusUnauthorized:
		return errors.New("the integration token was rejected by Notion; check KEST_SECRET_NOTION_TOKEN")
	case e.Code == "object_not_found" || status == http.StatusNotFound:
		return errors.New("that was not found, or the integration cannot see it: share the page or database with the integration (its ••• menu, Connections)")
	case status == http.StatusForbidden:
		return errors.New("the Notion integration does not have permission for this; check its capabilities (read, update, insert content)")
	case status == http.StatusTooManyRequests || status == 529:
		return errors.New("requests to Notion are being rate limited; try again in a minute")
	}
	msg := strings.Join(strings.Fields(e.Message), " ")
	if len(msg) > 200 {
		msg = msg[:200] + "…"
	}
	return fmt.Errorf("the Notion API answered with status %d: %s", status, msg)
}

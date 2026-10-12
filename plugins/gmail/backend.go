package gmail

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Harfull/Kest/plugins/internal/mailkit"
)

const (
	apiBase  = "https://gmail.googleapis.com/gmail/v1/users/me"
	tokenURL = "https://oauth2.googleapis.com/token" //nolint:gosec // an endpoint address, not a credential
	// maxResponse bounds any one API response. A raw message with a large
	// attachment can be big; it is refused rather than held in memory.
	maxResponse = 12 << 20
)

// errReauthorize explains how to fix a refresh token Google no longer accepts.
var errReauthorize = errors.New("the stored Gmail authorization is no longer accepted: it expired or was revoked. " +
	"Create a new refresh token and set KEST_SECRET_GMAIL_REFRESH_TOKEN (see docs/guides/gmail-setup.md)")

// credentials are the owner's OAuth client and refresh token. They are read at
// use time so a rotated secret is picked up on restart without code changes.
type credentials struct{ clientID, clientSecret, refreshToken string }

// backend implements mailkit.Backend over the Gmail REST API.
type backend struct {
	http     *http.Client
	now      func() time.Time
	creds    func() (credentials, error)
	maxChars int

	mu      sync.Mutex
	token   string
	expires time.Time
	address string // the account's own address, learned once
}

var _ mailkit.Backend = (*backend)(nil)

// accessToken returns a valid access token, refreshing it when needed.
func (b *backend) accessToken(ctx context.Context, force bool) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !force && b.token != "" && b.now().Before(b.expires) {
		return b.token, nil
	}
	c, err := b.creds()
	if err != nil {
		return "", err
	}
	form := url.Values{
		"grant_type": {"refresh_token"}, "client_id": {c.clientID}, "client_secret": {c.clientSecret}, "refresh_token": {c.refreshToken},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", errors.New("could not build the authorization request")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := b.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("reach Google to refresh the authorization: %s", reason(err))
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
	}
	_ = json.Unmarshal(body, &out)
	switch {
	case out.Error == "invalid_grant":
		return "", errReauthorize
	case out.Error == "invalid_client" || out.Error == "unauthorized_client":
		return "", errors.New("the OAuth client id or secret was rejected by Google; check KEST_SECRET_GMAIL_CLIENT_ID and KEST_SECRET_GMAIL_CLIENT_SECRET")
	case resp.StatusCode != http.StatusOK || out.AccessToken == "":
		return "", fmt.Errorf("no access token was issued by Google (status %d)", resp.StatusCode)
	}
	b.token = out.AccessToken
	b.expires = b.now().Add(time.Duration(max(out.ExpiresIn-60, 30)) * time.Second)
	return b.token, nil
}

func reason(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err.Error() // without the URL, which can carry query text
	}
	return err.Error()
}

// call makes one API request, retrying once with a fresh token after a 401.
func (b *backend) call(ctx context.Context, method, path string, query url.Values, in, out any) error {
	var payload []byte
	if in != nil {
		var err error
		if payload, err = json.Marshal(in); err != nil {
			return fmt.Errorf("encode the request: %w", err)
		}
	}
	for attempt := 0; ; attempt++ {
		token, err := b.accessToken(ctx, attempt > 0)
		if err != nil {
			return err
		}
		u := apiBase + path
		if len(query) > 0 {
			u += "?" + query.Encode()
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
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := b.http.Do(req)
		if err != nil {
			return fmt.Errorf("reach Gmail: %s", reason(err))
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
		_ = resp.Body.Close()
		if readErr != nil {
			return fmt.Errorf("read the Gmail response: %w", readErr)
		}
		if len(data) > maxResponse {
			return errors.New("the message is too large to read")
		}
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return apiError(resp.StatusCode, data)
		}
		if out != nil {
			if err := json.Unmarshal(data, out); err != nil {
				return errors.New("the Gmail response was not what was expected")
			}
		}
		return nil
	}
}

// apiError turns a Gmail error into a short message the owner can act on.
func apiError(status int, data []byte) error {
	var e struct {
		Error struct {
			Message string `json:"message"`
			Errors  []struct {
				Reason string `json:"reason"`
			} `json:"errors"`
		} `json:"error"`
	}
	_ = json.Unmarshal(data, &e)
	for _, x := range e.Error.Errors {
		switch x.Reason {
		case "insufficientPermissions", "ACCESS_TOKEN_SCOPE_INSUFFICIENT":
			return errors.New("the Gmail authorization does not include the permission for this (readonly reads, compose drafts, send sends, modify marks read); create a new refresh token with that scope")
		case "rateLimitExceeded", "userRateLimitExceeded":
			return errors.New("requests to Gmail are being rate limited; try again in a minute")
		}
	}
	switch status {
	case http.StatusNotFound:
		return errors.New("that message does not exist (it may have been deleted)")
	case http.StatusTooManyRequests:
		return errors.New("requests to Gmail are being rate limited; try again in a minute")
	}
	msg := strings.Join(strings.Fields(e.Error.Message), " ")
	if len(msg) > 160 {
		msg = msg[:160] + "…"
	}
	return fmt.Errorf("the Gmail API answered with status %d: %s", status, msg)
}

// gmailQuery builds a Gmail search string. Quotes are removed from terms so a
// value cannot end its own phrase and add operators.
func gmailQuery(q mailkit.Query, now time.Time) string {
	clean := func(s string) string {
		return strings.Join(strings.Fields(strings.NewReplacer(`"`, " ", "\\", " ").Replace(s)), " ")
	}
	var parts []string
	if t := clean(q.Text); t != "" {
		parts = append(parts, `"`+t+`"`)
	}
	if f := clean(q.From); f != "" {
		parts = append(parts, `from:"`+f+`"`)
	}
	if s := clean(q.Subject); s != "" {
		parts = append(parts, `subject:"`+s+`"`)
	}
	if q.UnreadOnly {
		parts = append(parts, "is:unread")
	}
	if q.SinceDays > 0 {
		parts = append(parts, fmt.Sprintf("after:%s", now.AddDate(0, 0, -q.SinceDays).Format("2006/01/02")))
	}
	return strings.Join(parts, " ")
}

type headerList []struct{ Name, Value string }

func (h headerList) get(name string) string {
	for _, x := range h {
		if strings.EqualFold(x.Name, name) {
			return x.Value
		}
	}
	return ""
}

// Search implements mailkit.Backend.
func (b *backend) Search(ctx context.Context, q mailkit.Query) ([]mailkit.Summary, error) {
	limit := q.Limit
	if limit <= 0 || limit > 20 {
		limit = 10
	}
	query := url.Values{"maxResults": {fmt.Sprint(limit)}}
	if s := gmailQuery(q, b.now()); s != "" {
		query.Set("q", s)
	}
	var list struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	if err := b.call(ctx, http.MethodGet, "/messages", query, nil, &list); err != nil {
		return nil, err
	}

	out := make([]mailkit.Summary, len(list.Messages))
	errs := make([]error, len(list.Messages))
	sem := make(chan struct{}, 5)
	var wg sync.WaitGroup
	for i, m := range list.Messages {
		wg.Add(1)
		go func() { // each goroutine owns one slot of out and errs; wg.Wait below is its stop condition
			defer wg.Done()
			defer func() { _ = recover() }()
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i], errs[i] = b.summary(ctx, m.ID)
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	// Gmail lists newest first already; keep that order but make it explicit.
	sort.SliceStable(out, func(i, j int) bool { return out[i].Date.After(out[j].Date) })
	return out, nil
}

func (b *backend) summary(ctx context.Context, id string) (mailkit.Summary, error) {
	query := url.Values{"format": {"metadata"}, "metadataHeaders": {"From", "Subject", "Date"}}
	var m struct {
		ID       string   `json:"id"`
		Labels   []string `json:"labelIds"`
		Snippet  string   `json:"snippet"`
		Payload  struct{ Headers headerList }
		Internal string `json:"internalDate"`
	}
	if err := b.call(ctx, http.MethodGet, "/messages/"+url.PathEscape(id), query, nil, &m); err != nil {
		return mailkit.Summary{}, err
	}
	// Rebuild a header block from the three values, with any line breaks inside a
	// value flattened so one value cannot start another header.
	oneLine := strings.NewReplacer("\r", " ", "\n", " ")
	hdr := "From: " + oneLine.Replace(m.Payload.Headers.get("From")) + "\r\nSubject: " + oneLine.Replace(m.Payload.Headers.get("Subject")) +
		"\r\nDate: " + oneLine.Replace(m.Payload.Headers.get("Date"))
	s, err := mailkit.ParseHeader([]byte(hdr))
	if err != nil {
		s = mailkit.Summary{Subject: "(unreadable message)"}
	}
	s.ID, s.Snippet = m.ID, strings.Join(strings.Fields(m.Snippet), " ")
	for _, l := range m.Labels {
		if l == "UNREAD" {
			s.Unread = true
		}
	}
	if s.Date.IsZero() {
		var ms int64
		if _, err := fmt.Sscan(m.Internal, &ms); err == nil && ms > 0 {
			s.Date = time.UnixMilli(ms).UTC()
		}
	}
	return s, nil
}

// Read implements mailkit.Backend.
func (b *backend) Read(ctx context.Context, id string) (mailkit.Message, error) {
	var m struct {
		ID       string   `json:"id"`
		ThreadID string   `json:"threadId"`
		Labels   []string `json:"labelIds"`
		Raw      string   `json:"raw"`
	}
	if err := b.call(ctx, http.MethodGet, "/messages/"+url.PathEscape(id), url.Values{"format": {"raw"}}, nil, &m); err != nil {
		return mailkit.Message{}, err
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(m.Raw, "="))
	if err != nil {
		return mailkit.Message{}, errors.New("the message from Gmail could not be decoded")
	}
	msg, err := mailkit.Parse(raw, b.maxChars)
	if err != nil {
		return mailkit.Message{}, err
	}
	msg.ID, msg.ThreadID = m.ID, m.ThreadID
	for _, l := range m.Labels {
		if l == "UNREAD" {
			msg.Unread = true
		}
	}
	return msg, nil
}

type rawBody struct {
	Raw      string `json:"raw"`
	ThreadID string `json:"threadId,omitempty"`
}

func encodeRaw(raw []byte, o mailkit.Outgoing) rawBody {
	body := rawBody{Raw: base64.RawURLEncoding.EncodeToString(raw)}
	if o.Reply != nil {
		body.ThreadID = o.Reply.ThreadID
	}
	return body
}

// Send implements mailkit.Backend.
func (b *backend) Send(ctx context.Context, raw []byte, o mailkit.Outgoing) error {
	return b.call(ctx, http.MethodPost, "/messages/send", nil, encodeRaw(raw, o), nil)
}

// Draft implements mailkit.Backend.
func (b *backend) Draft(ctx context.Context, raw []byte, o mailkit.Outgoing) error {
	return b.call(ctx, http.MethodPost, "/drafts", nil, map[string]any{"message": encodeRaw(raw, o)}, nil)
}

// MarkRead implements mailkit.Backend.
func (b *backend) MarkRead(ctx context.Context, id string) error {
	return b.call(ctx, http.MethodPost, "/messages/"+url.PathEscape(id)+"/modify", nil, map[string]any{"removeLabelIds": []string{"UNREAD"}}, nil)
}

// ownAddress returns the account's address, asking Gmail the first time.
func (b *backend) ownAddress(ctx context.Context) (string, error) {
	b.mu.Lock()
	known := b.address
	b.mu.Unlock()
	if known != "" {
		return known, nil
	}
	var p struct {
		EmailAddress string `json:"emailAddress"`
	}
	if err := b.call(ctx, http.MethodGet, "/profile", nil, nil, &p); err != nil {
		return "", err
	}
	if p.EmailAddress == "" {
		return "", errors.New("the account address was not reported by Gmail; set from_address")
	}
	b.mu.Lock()
	b.address = p.EmailAddress
	b.mu.Unlock()
	return p.EmailAddress, nil
}

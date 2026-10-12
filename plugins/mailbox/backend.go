package mailbox

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Harfull/Kest/plugins/internal/mailkit"
)

// settings is the plugin's resolved configuration.
type settings struct {
	imapHost, imapMode string
	imapPort           int
	smtpHost, smtpMode string
	smtpPort           int
	username           string
	from, fromName     string
	folder, drafts     string
	maxChars           int
}

// backend implements mailkit.Backend over IMAP and SMTP. Every operation opens
// its own connection: mail is checked a few times a day, so a connection kept
// alive would only be something else to expire and recover.
type backend struct {
	s        settings
	dial     dialFunc
	tls      *tls.Config // nil in production; tests trust their own certificate authority
	now      func() time.Time
	password func() (string, error)
}

var _ mailkit.Backend = (*backend)(nil)

func (b *backend) session(ctx context.Context, readOnly bool) (*imapClient, error) {
	pass, err := b.password()
	if err != nil {
		return nil, err
	}
	c, err := dialIMAP(ctx, b.dial, b.s.imapHost, b.s.imapPort, b.s.imapMode, b.tls)
	if err != nil {
		return nil, err
	}
	if err := c.login(ctx, b.s.username, pass); err != nil {
		c.close(ctx)
		return nil, err
	}
	if err := c.selectFolder(ctx, b.s.folder, readOnly); err != nil {
		c.close(ctx)
		return nil, err
	}
	return c, nil
}

func parseUID(id string) (uint32, error) {
	n, err := strconv.ParseUint(strings.TrimSpace(id), 10, 32)
	if err != nil || n == 0 {
		return 0, errors.New("that is not a message id from search_mail")
	}
	return uint32(n), nil
}

// Search implements mailkit.Backend.
func (b *backend) Search(ctx context.Context, q mailkit.Query) ([]mailkit.Summary, error) {
	c, err := b.session(ctx, true)
	if err != nil {
		return nil, err
	}
	defer c.close(ctx)

	var criteria []any
	add := func(parts ...any) { criteria = append(criteria, parts...) }
	if q.UnreadOnly {
		add("UNSEEN")
	}
	if q.SinceDays > 0 {
		add("SINCE", imapDate(b.now().AddDate(0, 0, -q.SinceDays)))
	}
	if q.From != "" {
		add("FROM", astring(q.From))
	}
	if q.Subject != "" {
		add("SUBJECT", astring(q.Subject))
	}
	if q.Text != "" {
		add("TEXT", astring(q.Text))
	}
	if len(criteria) == 0 {
		criteria = []any{"ALL"}
	}
	uids, err := c.uidSearch(ctx, criteria)
	if err != nil {
		return nil, err
	}
	sort.Slice(uids, func(i, j int) bool { return uids[i] > uids[j] }) // newest first
	limit := q.Limit
	if limit <= 0 || limit > 20 {
		limit = 10
	}
	if len(uids) > limit {
		uids = uids[:limit]
	}
	items, err := c.uidFetch(ctx, uids, "UID FLAGS BODY.PEEK[HEADER.FIELDS (FROM SUBJECT DATE MESSAGE-ID)]")
	if err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool { return items[i].uid > items[j].uid })
	out := make([]mailkit.Summary, 0, len(items))
	for _, it := range items {
		s, err := mailkit.ParseHeader(it.header)
		if err != nil {
			s = mailkit.Summary{Subject: "(unreadable message)"}
		}
		s.ID, s.Unread = strconv.FormatUint(uint64(it.uid), 10), !it.seen
		out = append(out, s)
	}
	return out, nil
}

// maxFetch bounds how much of one message is downloaded. The text is cut to
// the configured size afterwards; this keeps a message with a huge attachment
// from being pulled in full.
const maxFetch = 400 << 10

// Read implements mailkit.Backend.
func (b *backend) Read(ctx context.Context, id string) (mailkit.Message, error) {
	uid, err := parseUID(id)
	if err != nil {
		return mailkit.Message{}, err
	}
	c, err := b.session(ctx, true)
	if err != nil {
		return mailkit.Message{}, err
	}
	defer c.close(ctx)
	items, err := c.uidFetch(ctx, []uint32{uid}, fmt.Sprintf("UID FLAGS BODY.PEEK[]<0.%d>", maxFetch))
	if err != nil {
		return mailkit.Message{}, err
	}
	if len(items) == 0 || len(items[0].body) == 0 {
		return mailkit.Message{}, errors.New("that message no longer exists")
	}
	m, err := mailkit.Parse(items[0].body, b.s.maxChars)
	if err != nil {
		return mailkit.Message{}, err
	}
	m.ID, m.Unread = strconv.FormatUint(uint64(uid), 10), !items[0].seen
	return m, nil
}

// Send implements mailkit.Backend.
func (b *backend) Send(ctx context.Context, raw []byte, o mailkit.Outgoing) error {
	pass, err := b.password()
	if err != nil {
		return err
	}
	rcpts, err := mailkit.ValidateAddresses(append(append([]string(nil), o.To...), o.Cc...))
	if err != nil {
		return err
	}
	return sendSMTP(ctx, b.dial, b.tls, b.s.smtpHost, b.s.smtpPort, b.s.smtpMode, b.s.username, pass, b.s.from, rcpts, raw)
}

// Draft implements mailkit.Backend.
func (b *backend) Draft(ctx context.Context, raw []byte, _ mailkit.Outgoing) error {
	pass, err := b.password()
	if err != nil {
		return err
	}
	c, err := dialIMAP(ctx, b.dial, b.s.imapHost, b.s.imapPort, b.s.imapMode, b.tls)
	if err != nil {
		return err
	}
	defer c.close(ctx)
	if err := c.login(ctx, b.s.username, pass); err != nil {
		return err
	}
	return c.appendMessage(ctx, b.s.drafts, `\Draft \Seen`, raw)
}

// MarkRead implements mailkit.Backend.
func (b *backend) MarkRead(ctx context.Context, id string) error {
	uid, err := parseUID(id)
	if err != nil {
		return err
	}
	c, err := b.session(ctx, false)
	if err != nil {
		return err
	}
	defer c.close(ctx)
	return c.markSeen(ctx, uid)
}

// imapDate formats a date as IMAP wants it: 2-Jan-2006, always English months.
func imapDate(t time.Time) string {
	months := [...]string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}
	return fmt.Sprintf("%d-%s-%d", t.Day(), months[t.Month()-1], t.Year())
}

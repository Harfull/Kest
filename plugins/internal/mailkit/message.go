// Package mailkit holds what the email plugins share: the message types, MIME
// parsing and composing, the tool definitions and the opt-in inbox nudges.
//
// The mailbox plugin (IMAP and SMTP) and the gmail plugin (Gmail API) each
// implement Backend and get the same tools, so the model sees one email
// vocabulary whichever account it uses. It is not a mail client: it never
// opens a connection itself.
package mailkit

import (
	"strings"
	"time"
	"unicode"
)

// Summary is one line of a search result.
type Summary struct {
	ID      string
	From    string
	Subject string
	Date    time.Time
	Unread  bool
	Snippet string
}

// Message is a parsed email.
type Message struct {
	Summary
	To, Cc      []string
	MessageID   string   // the Message-ID header, for replies
	References  []string // the References header, for replies
	ThreadID    string   // set by backends that have threads
	Text        string   // body as plain text
	Attachments []string // file names, never contents
}

// Query describes a search. Empty fields do not constrain it.
type Query struct {
	Text       string
	From       string
	Subject    string
	UnreadOnly bool
	SinceDays  int
	Limit      int
}

// Outgoing is a message to send or save as a draft.
type Outgoing struct {
	To, Cc  []string
	Subject string
	Body    string
	// Reply, if set, is the message being answered; threading headers come from it.
	Reply *Message
}

// plain removes control characters, which have no place in a label or a prompt
// shown to the owner, and collapses runs of whitespace.
func plain(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return ' '
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}

func oneLine(s string) string { return strings.Join(strings.Fields(plain(s)), " ") }

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

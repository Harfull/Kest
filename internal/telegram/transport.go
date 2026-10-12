// Package telegram connects Kest to Telegram.
//
// The rest of the system sees only the Transport interface and the plain types
// in this file; the third-party library stays behind bot.go so it can be
// replaced without touching callers. The transport drops every update that
// does not come from a configured owner before anything else can see it.
package telegram

import (
	"context"
	"errors"
)

// Update is one thing the owner did: sent text, sent a voice note, ran a
// command or pressed an inline button. Exactly one of Text, Voice, Command or
// Callback is meaningful.
type Update struct {
	ChatID    int64
	UserID    int64
	MessageID int

	Text    string // plain message text, empty for commands and voice notes
	Command string // lower-case command without slash or @bot suffix, e.g. "status"
	Args    string // text after the command

	Voice    *Voice
	Callback *Callback
}

// Voice describes a voice note. Fetch the audio with Transport.Download.
type Voice struct {
	FileID   string
	Duration int    // seconds
	MIMEType string // usually audio/ogg
	Size     int64  // bytes, may be zero if Telegram did not report it
}

// Callback is an inline button press.
type Callback struct {
	ID        string // pass to AnswerCallback
	Data      string // the data set on the button
	MessageID int    // the message that carries the button
}

// Button is an inline keyboard button. Data is limited to 64 bytes by Telegram.
type Button struct {
	Label string
	Data  string
}

// Reply is an outgoing text message.
type Reply struct {
	ChatID int64
	// Text is plain text with light markup: **bold** and `code`. It is escaped
	// and converted for Telegram, and split if it exceeds the message limit.
	Text string
	// Buttons are rows of inline buttons, attached to the last part.
	Buttons [][]Button
	// Silent sends without a notification sound.
	Silent bool
	// ReplyTo quotes an earlier message when non-zero.
	ReplyTo int
}

// Command is an entry in the bot's slash-command menu.
type Command struct {
	Name        string // 1 to 32 lower-case letters, digits or underscores
	Description string // 1 to 256 characters
}

// Handler receives owner updates. The transport calls it one update at a time,
// in arrival order, so it must return quickly: hand slow work (a model call) to
// its own goroutine so that a callback press is not stuck behind a message.
type Handler func(ctx context.Context, u Update)

// Transport is everything Kest needs from a chat channel.
type Transport interface {
	// Run delivers updates to h until ctx is cancelled.
	Run(ctx context.Context, h Handler) error
	// Send posts a text reply and returns the ID of its last part.
	Send(ctx context.Context, r Reply) (messageID int, err error)
	// SendVoice posts audio as a voice message. ext is the file type: "ogg" for
	// Ogg/Opus (Telegram's native voice format) or "mp3".
	SendVoice(ctx context.Context, chatID int64, audio []byte, ext string, silent bool) error
	// SendDocument posts a file, such as a data export.
	SendDocument(ctx context.Context, chatID int64, filename string, data []byte, caption string) error
	// Typing shows "typing…" (or "recording voice…") for a few seconds.
	Typing(ctx context.Context, chatID int64, recording bool) error
	// Download fetches a file, such as a voice note, up to Telegram's 20 MB limit.
	Download(ctx context.Context, fileID string) ([]byte, error)
	// AnswerCallback acknowledges a button press, optionally with a short toast.
	AnswerCallback(ctx context.Context, callbackID, text string) error
	// ClearButtons removes the inline keyboard from a message.
	ClearButtons(ctx context.Context, chatID int64, messageID int) error
	// SetCommands registers the slash-command menu.
	SetCommands(ctx context.Context, cmds []Command) error
}

// ErrFileNotFound is returned by Fake.Download for unknown file IDs.
var ErrFileNotFound = errors.New("telegram: file not found")

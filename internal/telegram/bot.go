package telegram

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

const (
	// maxDownloadBytes is the Bot API's download limit for getFile.
	maxDownloadBytes = 20 << 20
	// pollTimeout is the long-poll duration for getUpdates.
	pollTimeout = 30 * time.Second
)

// Options configures a Bot.
type Options struct {
	Token    string
	OwnerIDs []int64
	Log      *slog.Logger

	// ServerURL overrides https://api.telegram.org. Tests point it at a fake.
	ServerURL string
	// HTTP is the client for Bot API calls and file downloads. Its timeout must
	// exceed the 30 second long-poll duration.
	HTTP *http.Client
}

// Bot is the Telegram Transport, built on github.com/go-telegram/bot.
type Bot struct {
	b      *tgbot.Bot
	token  string
	owners map[int64]bool
	log    *slog.Logger
	http   *http.Client

	handler atomic.Pointer[Handler]
	dropped atomic.Int64
}

var _ Transport = (*Bot)(nil)

// New connects to Telegram, verifying the token with getMe.
func New(opts Options) (*Bot, error) {
	if len(opts.OwnerIDs) == 0 {
		return nil, errors.New("telegram: at least one owner ID is required")
	}
	if opts.HTTP == nil {
		opts.HTTP = &http.Client{Timeout: pollTimeout + 15*time.Second}
	}
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}

	owners := make(map[int64]bool, len(opts.OwnerIDs))
	for _, id := range opts.OwnerIDs {
		owners[id] = true
	}

	t := &Bot{token: opts.Token, owners: owners, log: opts.Log, http: opts.HTTP}

	options := []tgbot.Option{
		tgbot.WithHTTPClient(pollTimeout, opts.HTTP),
		tgbot.WithDefaultHandler(t.dispatch),
		tgbot.WithNotAsyncHandlers(), // deliver updates one at a time, in order
		tgbot.WithAllowedUpdates(tgbot.AllowedUpdates{"message", "callback_query"}),
		tgbot.WithErrorsHandler(func(err error) {
			// Library errors can embed the request URL, which contains the
			// token; the logger's redactor masks it.
			opts.Log.Warn("telegram error", "err", err)
		}),
	}
	if opts.ServerURL != "" {
		options = append(options, tgbot.WithServerURL(opts.ServerURL))
	}

	b, err := tgbot.New(opts.Token, options...)
	if err != nil {
		return nil, fmt.Errorf("telegram: connect: %w", scrub(err, opts.Token))
	}
	t.b = b
	return t, nil
}

// Run delivers owner updates to h until ctx is cancelled.
func (t *Bot) Run(ctx context.Context, h Handler) error {
	t.handler.Store(&h)
	t.b.Start(ctx)
	return nil
}

// Dropped returns how many updates from non-owners were discarded.
func (t *Bot) Dropped() int64 { return t.dropped.Load() }

// dispatch is the library's default handler. It is the only entry point for
// updates, so the owner check here guards everything downstream.
func (t *Bot) dispatch(ctx context.Context, _ *tgbot.Bot, upd *models.Update) {
	u, ok := t.normalize(upd)
	if !ok {
		return
	}
	if h := t.handler.Load(); h != nil {
		(*h)(ctx, u)
	}
}

// normalize converts a library update into an Update, returning false for
// updates that are not from an owner in a private chat or are not supported.
func (t *Bot) normalize(upd *models.Update) (Update, bool) {
	switch {
	case upd.Message != nil:
		m := upd.Message
		if m.From == nil || !t.owners[m.From.ID] || m.Chat.Type != models.ChatTypePrivate {
			t.dropped.Add(1)
			return Update{}, false
		}
		u := Update{ChatID: m.Chat.ID, UserID: m.From.ID, MessageID: m.ID, ReplyTo: quoted(m)}
		switch {
		case m.Voice != nil:
			u.Voice = &Voice{FileID: m.Voice.FileID, Duration: m.Voice.Duration, MIMEType: m.Voice.MimeType, Size: m.Voice.FileSize}
		case len(m.Photo) > 0:
			u.Photo = pickPhoto(m.Photo)
			u.Text = strings.TrimSpace(m.Caption)
		case strings.HasPrefix(m.Text, "/"):
			u.Command, u.Args = parseCommand(m.Text)
		case strings.TrimSpace(m.Text) != "":
			u.Text = m.Text
		default:
			return Update{}, false // stickers and the like are not supported yet
		}
		return u, true

	case upd.CallbackQuery != nil:
		q := upd.CallbackQuery
		if !t.owners[q.From.ID] || q.Message.Message == nil {
			t.dropped.Add(1)
			return Update{}, false
		}
		return Update{
			ChatID: q.Message.Message.Chat.ID,
			UserID: q.From.ID,
			Callback: &Callback{
				ID:        q.ID,
				Data:      q.Data,
				MessageID: q.Message.Message.ID,
			},
		}, true
	}
	return Update{}, false
}

// maxPhotoInput is the largest photo Kest downloads to show a model. Telegram
// offers several sizes of every photo; the biggest one under this is used.
const maxPhotoInput = 5 << 20

// pickPhoto chooses the largest size that is small enough to send to a model.
func pickPhoto(sizes []models.PhotoSize) *Photo {
	var best *models.PhotoSize
	for i := range sizes {
		s := &sizes[i]
		if s.FileSize > maxPhotoInput {
			continue
		}
		if best == nil || s.Width*s.Height > best.Width*best.Height {
			best = s
		}
	}
	if best == nil {
		return nil // every size is too large; the text of the message still goes through
	}
	return &Photo{FileID: best.FileID, Size: int64(best.FileSize)}
}

// quoted describes the message m replies to, or nil.
func quoted(m *models.Message) *Quoted {
	r := m.ReplyToMessage
	if r == nil {
		return nil
	}
	q := &Quoted{MessageID: r.ID, FromBot: r.From != nil && r.From.IsBot, Forwarded: r.ForwardOrigin != nil}
	q.Text = strings.TrimSpace(r.Text)
	if q.Text == "" {
		q.Text = strings.TrimSpace(r.Caption)
	}
	if m.Quote != nil && strings.TrimSpace(m.Quote.Text) != "" {
		q.Text = strings.TrimSpace(m.Quote.Text) // the owner highlighted this part
	}
	if len(r.Photo) > 0 {
		q.Photo = pickPhoto(r.Photo)
	}
	return q
}

// parseCommand splits "/Status@MyBot  now" into ("status", "now").
func parseCommand(text string) (cmd, args string) {
	head, rest, _ := strings.Cut(strings.TrimSpace(text), " ")
	head = strings.TrimPrefix(head, "/")
	head, _, _ = strings.Cut(head, "@")
	return strings.ToLower(head), strings.TrimSpace(rest)
}

// Send posts a text reply, splitting long text and attaching buttons to the
// last part. If Telegram rejects the formatting, the text is resent as plain.
func (t *Bot) Send(ctx context.Context, r Reply) (int, error) {
	if err := validateButtons(r.Buttons); err != nil {
		return 0, fmt.Errorf("telegram: %w", err)
	}
	parts := Split(r.Text, splitAt)
	if len(parts) == 0 {
		return 0, errors.New("telegram: empty message")
	}

	lastID := 0
	for i, part := range parts {
		params := &tgbot.SendMessageParams{
			ChatID:              r.ChatID,
			Text:                ToHTML(part),
			ParseMode:           models.ParseModeHTML,
			DisableNotification: r.Silent,
			LinkPreviewOptions:  &models.LinkPreviewOptions{IsDisabled: tgbot.True()},
		}
		if i == 0 && r.ReplyTo != 0 {
			params.ReplyParameters = &models.ReplyParameters{MessageID: r.ReplyTo, AllowSendingWithoutReply: true}
		}
		if i == len(parts)-1 && len(r.Buttons) > 0 {
			params.ReplyMarkup = keyboard(r.Buttons)
		}

		msg, err := t.b.SendMessage(ctx, params)
		if err != nil && strings.Contains(err.Error(), "can't parse entities") {
			params.Text, params.ParseMode = part, ""
			msg, err = t.b.SendMessage(ctx, params)
		}
		if err != nil {
			return lastID, fmt.Errorf("telegram: send message: %w", scrub(err, t.token))
		}
		lastID = msg.ID
	}
	return lastID, nil
}

func keyboard(rows [][]Button) *models.InlineKeyboardMarkup {
	kb := &models.InlineKeyboardMarkup{}
	for _, row := range rows {
		var out []models.InlineKeyboardButton
		for _, b := range row {
			out = append(out, models.InlineKeyboardButton{Text: b.Label, CallbackData: b.Data})
		}
		kb.InlineKeyboard = append(kb.InlineKeyboard, out)
	}
	return kb
}

// SendVoice posts audio as a voice message. Telegram plays Ogg/Opus, MP3 and
// M4A sent this way inline.
func (t *Bot) SendVoice(ctx context.Context, chatID int64, audio []byte, ext string, silent bool) error {
	if len(audio) == 0 {
		return errors.New("telegram: empty audio")
	}
	if ext != "ogg" && ext != "mp3" && ext != "m4a" {
		return fmt.Errorf("telegram: voice messages must be ogg, mp3 or m4a, not %q", ext)
	}
	_, err := t.b.SendVoice(ctx, &tgbot.SendVoiceParams{
		ChatID:              chatID,
		Voice:               &models.InputFileUpload{Filename: "kest." + ext, Data: bytes.NewReader(audio)},
		DisableNotification: silent,
	})
	if err != nil {
		return fmt.Errorf("telegram: send voice: %w", scrub(err, t.token))
	}
	return nil
}

// SendDocument posts a file.
func (t *Bot) SendDocument(ctx context.Context, chatID int64, filename string, data []byte, caption string) error {
	_, err := t.b.SendDocument(ctx, &tgbot.SendDocumentParams{
		ChatID:   chatID,
		Document: &models.InputFileUpload{Filename: filename, Data: bytes.NewReader(data)},
		Caption:  caption,
	})
	if err != nil {
		return fmt.Errorf("telegram: send document: %w", scrub(err, t.token))
	}
	return nil
}

// Typing shows a chat action for about five seconds.
func (t *Bot) Typing(ctx context.Context, chatID int64, recording bool) error {
	action := models.ChatActionTyping
	if recording {
		action = models.ChatActionRecordVoice
	}
	if _, err := t.b.SendChatAction(ctx, &tgbot.SendChatActionParams{ChatID: chatID, Action: action}); err != nil {
		return fmt.Errorf("telegram: send chat action: %w", scrub(err, t.token))
	}
	return nil
}

// Download fetches a file by ID, enforcing the 20 MB Bot API limit.
func (t *Bot) Download(ctx context.Context, fileID string) ([]byte, error) {
	f, err := t.b.GetFile(ctx, &tgbot.GetFileParams{FileID: fileID})
	if err != nil {
		return nil, fmt.Errorf("telegram: get file: %w", scrub(err, t.token))
	}
	if f.FileSize > maxDownloadBytes {
		return nil, fmt.Errorf("telegram: file is %d bytes, over the %d byte download limit", f.FileSize, maxDownloadBytes)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.b.FileDownloadLink(f), nil)
	if err != nil {
		return nil, fmt.Errorf("telegram: build download request: %w", scrub(err, t.token))
	}
	resp, err := t.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("telegram: download: %w", scrub(err, t.token))
	}
	defer resp.Body.Close() //nolint:errcheck // body is fully read below

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("telegram: download: status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDownloadBytes+1))
	if err != nil {
		return nil, fmt.Errorf("telegram: read download: %w", scrub(err, t.token))
	}
	if len(data) > maxDownloadBytes {
		return nil, errors.New("telegram: download exceeds the 20 MB limit")
	}
	return data, nil
}

// AnswerCallback acknowledges a button press.
func (t *Bot) AnswerCallback(ctx context.Context, callbackID, text string) error {
	_, err := t.b.AnswerCallbackQuery(ctx, &tgbot.AnswerCallbackQueryParams{CallbackQueryID: callbackID, Text: text})
	if err != nil {
		return fmt.Errorf("telegram: answer callback: %w", scrub(err, t.token))
	}
	return nil
}

// ClearButtons removes a message's inline keyboard.
func (t *Bot) ClearButtons(ctx context.Context, chatID int64, messageID int) error {
	_, err := t.b.EditMessageReplyMarkup(ctx, &tgbot.EditMessageReplyMarkupParams{
		ChatID:      chatID,
		MessageID:   messageID,
		ReplyMarkup: &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{}},
	})
	if err != nil {
		return fmt.Errorf("telegram: clear buttons: %w", scrub(err, t.token))
	}
	return nil
}

// SetCommands registers the slash-command menu.
func (t *Bot) SetCommands(ctx context.Context, cmds []Command) error {
	list := make([]models.BotCommand, len(cmds))
	for i, c := range cmds {
		list[i] = models.BotCommand{Command: c.Name, Description: c.Description}
	}
	if _, err := t.b.SetMyCommands(ctx, &tgbot.SetMyCommandsParams{Commands: list}); err != nil {
		return fmt.Errorf("telegram: set commands: %w", scrub(err, t.token))
	}
	return nil
}

// scrub removes the bot token from errors that embed a request URL, which
// net/http includes in transport errors.
func scrub(err error, token string) error {
	if err == nil {
		return nil
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s request failed: %w", ue.Op, ue.Err)
	}
	if token != "" && strings.Contains(err.Error(), token) {
		return errors.New(strings.ReplaceAll(err.Error(), token, "[redacted]"))
	}
	return err
}

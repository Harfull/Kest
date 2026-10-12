package app

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/Harfull/Kest/internal/agent"
	"github.com/Harfull/Kest/internal/audio"
	"github.com/Harfull/Kest/internal/clock"
	"github.com/Harfull/Kest/internal/config"
	"github.com/Harfull/Kest/internal/llm"
	"github.com/Harfull/Kest/internal/plugin"
	"github.com/Harfull/Kest/internal/scheduler"
	"github.com/Harfull/Kest/internal/store"
	"github.com/Harfull/Kest/internal/telegram"
	"github.com/Harfull/Kest/internal/version"
)

const (
	// queueSize bounds pending conversation turns. The owner is one person, so
	// a full queue means something is wrong and shedding load is right.
	queueSize = 16
	// maxVoiceReplyChars: longer answers are sent as text, because a long
	// spoken reply is slow to listen to and costs more to synthesize.
	maxVoiceReplyChars = 1000
	// maxVoiceDownload matches the Bot API's 20 MB download limit.
	maxVoiceDownload = 20 << 20
	// confirmTTL is how long an inline confirmation stays valid.
	confirmTTL = 10 * time.Minute
)

// chat turns Telegram updates into conversation turns and commands.
type chat struct {
	cfg     config.Config
	log     *slog.Logger
	clock   clock.Clock
	loc     *time.Location
	tr      telegram.Transport
	agent   *agent.Agent
	llm     *llm.Client
	db      *store.DB
	sched   *scheduler.Scheduler
	budget  *llm.Budget
	started time.Time

	ownerChat int64 // where approvals and plugin notifications go
	confirm   *agent.Confirmations
	registry  *plugin.Registry

	// queue serializes conversation turns so history stays in order, while
	// commands and button presses are handled immediately.
	queue chan func(context.Context)
}

// handle is the telegram.Handler. It must return quickly.
func (c *chat) handle(ctx context.Context, u telegram.Update) {
	switch {
	case u.Callback != nil:
		c.callback(ctx, u)
	case u.Command != "":
		c.command(ctx, u)
	case u.Voice != nil:
		c.enqueue(ctx, u.ChatID, func(ctx context.Context) { c.voiceTurn(ctx, u) })
	case u.Text != "":
		c.enqueue(ctx, u.ChatID, func(ctx context.Context) { c.textTurn(ctx, u.ChatID, u.Text, false) })
	}
}

// work runs queued turns one at a time until ctx is cancelled.
func (c *chat) work(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-c.queue:
			c.safely(ctx, job)
		}
	}
}

// safely runs a job and contains panics so one bad turn cannot stop the bot.
func (c *chat) safely(ctx context.Context, job func(context.Context)) {
	defer func() {
		if p := recover(); p != nil {
			c.log.ErrorContext(ctx, "conversation turn panicked", "panic", fmt.Sprint(p))
		}
	}()
	job(ctx)
}

func (c *chat) enqueue(ctx context.Context, chatID int64, job func(context.Context)) {
	select {
	case c.queue <- job:
	default:
		c.log.WarnContext(ctx, "conversation queue full; shedding a message")
		c.say(ctx, chatID, msgBusy)
	}
}

// say sends a plain reply and logs failures.
func (c *chat) say(ctx context.Context, chatID int64, text string) {
	if _, err := c.tr.Send(ctx, telegram.Reply{ChatID: chatID, Text: text}); err != nil {
		c.log.ErrorContext(ctx, "send reply", "err", err)
	}
}

// textTurn answers one message. viaVoice makes the reply a voice note when it
// is short enough.
func (c *chat) textTurn(ctx context.Context, chatID int64, text string, viaVoice bool) {
	if err := c.tr.Typing(ctx, chatID, false); err != nil {
		c.log.DebugContext(ctx, "typing indicator failed", "err", err)
	}

	reply, err := c.agent.Reply(ctx, text)
	if err != nil {
		c.log.ErrorContext(ctx, "conversation turn failed", "err", err)
		if reply == "" {
			c.say(ctx, chatID, msgModelDown)
			return
		}
		// The reply exists but could not be stored; still deliver it.
	}

	if viaVoice && len([]rune(reply)) <= maxVoiceReplyChars {
		if c.sendVoice(ctx, chatID, reply) {
			return
		}
	}
	c.say(ctx, chatID, reply)
}

// sendVoice synthesizes and sends reply, reporting whether it was delivered so
// the caller can fall back to text.
func (c *chat) sendVoice(ctx context.Context, chatID int64, reply string) bool {
	if err := c.tr.Typing(ctx, chatID, true); err != nil {
		c.log.DebugContext(ctx, "recording indicator failed", "err", err)
	}
	speech, err := c.llm.Speak(ctx, plainText(reply))
	if err != nil {
		c.log.WarnContext(ctx, "speech synthesis failed; sending text", "err", err)
		return false
	}
	data, ext, err := voiceFile(speech)
	if err != nil {
		c.log.WarnContext(ctx, "could not prepare the voice reply; sending text", "err", err)
		return false
	}
	if err := c.tr.SendVoice(ctx, chatID, data, ext, false); err != nil {
		c.log.WarnContext(ctx, "voice send failed; sending text", "err", err)
		return false
	}
	return true
}

// plainText strips the light markup so it is not read aloud.
func plainText(s string) string {
	return strings.NewReplacer("**", "", "`", "").Replace(s)
}

// voiceTurn transcribes a voice note and answers it.
func (c *chat) voiceTurn(ctx context.Context, u telegram.Update) {
	if u.Voice.Size > maxVoiceDownload {
		c.say(ctx, u.ChatID, msgVoiceTooBig)
		return
	}
	if err := c.tr.Typing(ctx, u.ChatID, false); err != nil {
		c.log.DebugContext(ctx, "typing indicator failed", "err", err)
	}

	audio, err := c.tr.Download(ctx, u.Voice.FileID)
	if err != nil {
		c.log.ErrorContext(ctx, "download voice note", "err", err)
		c.say(ctx, u.ChatID, msgNoVoice)
		return
	}

	text, err := c.llm.Transcribe(ctx, audio, voiceFormat(u.Voice.MIMEType), "")
	if err != nil {
		c.log.ErrorContext(ctx, "transcribe voice note", "err", err)
		c.say(ctx, u.ChatID, msgNoVoice)
		return
	}
	if text == "" {
		c.say(ctx, u.ChatID, msgNoVoice)
		return
	}
	c.textTurn(ctx, u.ChatID, text, true)
}

// voiceFormat maps a MIME type to the audio format name the API expects.
// Telegram voice notes are OGG/Opus.
func voiceFormat(mime string) string {
	switch strings.ToLower(mime) {
	case "audio/mpeg", "audio/mp3":
		return "mp3"
	case "audio/mp4", "audio/m4a", "audio/x-m4a":
		return "m4a"
	case "audio/wav", "audio/x-wav":
		return "wav"
	default:
		return "ogg"
	}
}

// --- commands ----------------------------------------------------------------

func (c *chat) command(ctx context.Context, u telegram.Update) {
	switch u.Command {
	case "start":
		c.say(ctx, u.ChatID, msgStart)
	case "help":
		c.say(ctx, u.ChatID, msgHelp)
	case "status":
		c.status(ctx, u.ChatID)
	case "export":
		c.export(ctx, u.ChatID)
	case "forget":
		c.forgetAsk(ctx, u.ChatID)
	default:
		c.say(ctx, u.ChatID, msgUnknownCommand)
	}
}

func (c *chat) status(ctx context.Context, chatID int64) {
	var b strings.Builder
	fmt.Fprintf(&b, "**Kest** %s, up %s.\n", version.Version, c.clock.Now().Sub(c.started).Round(time.Minute))

	spent, err := c.budget.Spent(ctx)
	switch {
	case err != nil:
		b.WriteString("Spend today: unavailable.\n")
	case c.budget.Limit() > 0:
		fmt.Fprintf(&b, "Spend today: $%.2f of $%.2f.\n", spent, c.budget.Limit())
	default:
		fmt.Fprintf(&b, "Spend today: $%.2f (no limit set).\n", spent)
	}

	fmt.Fprintf(&b, "Model: %s\n", c.cfg.LLM.Models.General)

	jobs, err := c.sched.Upcoming(ctx, 3)
	switch {
	case err != nil:
		b.WriteString("Scheduled: unavailable.\n")
	case len(jobs) == 0:
		b.WriteString("Scheduled: nothing yet.\n")
	default:
		b.WriteString("Next up:\n")
		for _, j := range jobs {
			fmt.Fprintf(&b, "- %s at %s\n", j.Name, j.NextRunAt.In(c.loc).Format("Mon 2 Jan, 15:04"))
		}
	}

	b.WriteString(c.pluginLine())

	if d, ok := c.tr.(interface{ Dropped() int64 }); ok && d.Dropped() > 0 {
		fmt.Fprintf(&b, "Messages ignored from other accounts: %d.\n", d.Dropped())
	}
	c.say(ctx, chatID, strings.TrimSpace(b.String()))
}

func (c *chat) export(ctx context.Context, chatID int64) {
	data, err := c.db.Export(ctx, c.clock.Now())
	if err != nil {
		c.log.ErrorContext(ctx, "export data", "err", err)
		c.say(ctx, chatID, msgExportFailed)
		return
	}
	name := "kest-export-" + c.clock.Now().In(c.loc).Format("2006-01-02") + ".json"
	if err := c.tr.SendDocument(ctx, chatID, name, data, "Your data. Keep it somewhere private."); err != nil {
		c.log.ErrorContext(ctx, "send export", "err", err)
		c.say(ctx, chatID, msgExportFailed)
	}
}

// --- /forget confirmation ------------------------------------------------------

// Button data is "forget:<yes|no>:<unix seconds>". The timestamp makes old
// buttons expire without any server-side state.
const forgetPrefix = "forget:"

func (c *chat) forgetAsk(ctx context.Context, chatID int64) {
	stamp := strconv.FormatInt(c.clock.Now().Unix(), 10)
	_, err := c.tr.Send(ctx, telegram.Reply{
		ChatID: chatID,
		Text:   msgForgetAsk,
		Buttons: [][]telegram.Button{{
			{Label: "Delete it", Data: forgetPrefix + "yes:" + stamp},
			{Label: "Cancel", Data: forgetPrefix + "no:" + stamp},
		}},
	})
	if err != nil {
		c.log.ErrorContext(ctx, "send forget confirmation", "err", err)
	}
}

func (c *chat) callback(ctx context.Context, u telegram.Update) {
	cb := u.Callback
	ack := func(text string) {
		if err := c.tr.AnswerCallback(ctx, cb.ID, text); err != nil {
			c.log.DebugContext(ctx, "answer callback", "err", err)
		}
	}

	if id, approve, ok := agent.ParseDecision(cb.Data); ok {
		c.decide(ctx, ack, id, approve)
		return
	}

	choice, stamp, ok := parseForget(cb.Data)
	if !ok {
		ack("")
		return
	}
	if c.clock.Now().Sub(time.Unix(stamp, 0)) > confirmTTL {
		ack("")
		c.clearButtons(ctx, u.ChatID, cb.MessageID)
		c.say(ctx, u.ChatID, msgForgetExpired)
		return
	}

	c.clearButtons(ctx, u.ChatID, cb.MessageID)
	if choice != "yes" {
		ack("")
		c.say(ctx, u.ChatID, msgForgetCancel)
		return
	}

	if err := c.db.Forget(ctx); err != nil {
		c.log.ErrorContext(ctx, "forget data", "err", err)
		ack("")
		c.say(ctx, u.ChatID, msgGenericFail)
		return
	}
	if err := c.db.AddAudit(ctx, store.AuditEntry{
		TS: c.clock.Now(), Actor: "owner", Action: "forget", Risk: "destructive", Confirmed: true, Status: "ok",
		Summary: "deleted conversation history and preferences",
	}); err != nil {
		c.log.WarnContext(ctx, "audit forget", "err", err)
	}
	ack("Deleted")
	c.say(ctx, u.ChatID, msgForgetDone)
}

func (c *chat) clearButtons(ctx context.Context, chatID int64, messageID int) {
	if err := c.tr.ClearButtons(ctx, chatID, messageID); err != nil {
		c.log.DebugContext(ctx, "clear buttons", "err", err)
	}
}

// parseForget extracts the choice and timestamp from forget button data.
func parseForget(data string) (choice string, stamp int64, ok bool) {
	rest, found := strings.CutPrefix(data, forgetPrefix)
	if !found {
		return "", 0, false
	}
	choice, ts, found := strings.Cut(rest, ":")
	if !found || (choice != "yes" && choice != "no") {
		return "", 0, false
	}
	n, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return "", 0, false
	}
	return choice, n, true
}

// decide handles an approval button press.
func (c *chat) decide(ctx context.Context, ack func(string), id string, approve bool) {
	if !c.confirm.Resolve(ctx, id, approve) {
		ack("That one has expired.")
		return
	}
	if approve {
		ack("Going ahead")
	} else {
		ack("Skipped")
	}
}

// pluginLine summarizes which plugins are running and which failed.
func (c *chat) pluginLine() string {
	var ok, bad []string
	for _, st := range c.registry.Statuses() {
		if st.Err != nil {
			bad = append(bad, st.ID)
		} else {
			ok = append(ok, st.ID)
		}
	}
	line := "Plugins: none loaded.\n"
	if len(ok) > 0 {
		line = "Plugins: " + strings.Join(ok, ", ") + ".\n"
	}
	if len(bad) > 0 {
		line += "Could not load: " + strings.Join(bad, ", ") + " (see the logs).\n"
	}
	return line
}

// voiceFile turns synthesized speech into a file Telegram plays as a voice
// message: MP3 is sent as is, PCM is encoded to Ogg/Opus.
func voiceFile(s *llm.Speech) (data []byte, ext string, err error) {
	if s.Format == llm.FormatPCM {
		ogg, err := audio.PCMToOggOpus(s.Data, s.SampleRate, s.Channels)
		if err != nil {
			return nil, "", err
		}
		return ogg, "ogg", nil
	}
	return s.Data, "mp3", nil
}

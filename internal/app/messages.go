package app

import "github.com/Harfull/Kest/internal/telegram"

// User-facing text. Every string follows docs/persona/voice.md: calm, direct,
// warm, no emoji, no exclamation marks, no guilt.
const (
	msgStart = "Hi, I'm Kest. I'm here to help you stay consistent with the things you care about: training, meals, sleep and focus.\n\n" +
		"Send me a message or a voice note any time. Send /help to see what I can do."

	msgHelp = "You can talk to me by text or voice. I reply in the same way you wrote.\n\n" +
		"/status    how I'm doing and what's scheduled\n" +
		"/settings  how often and when I check in\n" +
		"/export    get a copy of your data\n" +
		"/forget    delete our conversation history\n" +
		"/lockdown  stop writing and unprompted messages until restart\n" +
		"/help      this list\n\n" +
		"I can also set reminders, keep a task list, read a web page for you and remember things about you. Just ask."

	msgModelDown = "I couldn't reach my language model just now. Try again in a minute; your message is saved."

	msgBusy = "I'm a little behind. Give me a moment and send that again."

	msgNoVoice = "I couldn't make out that voice note. Could you try again, or type it?"

	msgVoiceTooBig = "That voice note is too long for me to download. A shorter one, or text, will work."

	msgUnknownCommand = "I don't know that one. /help lists what I can do."

	msgForgetAsk = "This deletes our conversation history, what I remember about you, and your saved preferences. Your tasks, scheduled jobs and cost records stay. " +
		"It can't be undone. Go ahead?"

	msgForgetDone = "Done. Our conversation history, my notes about you and your preferences are deleted."

	msgForgetCancel = "Okay, nothing was deleted."

	msgForgetExpired = "That confirmation has expired. Send /forget again if you still want to."

	msgExportFailed = "I couldn't prepare your export. Please try again in a moment."

	msgApprovalTimedOut = "I did not hear back, so I skipped that. Ask again if you still want it."

	msgLockdown = "Lockdown is on. Until I restart I will only read: nothing gets written, deleted or sent, and I will not message you unprompted. " +
		"If you think your accounts are at risk, rotate the Telegram token and the OpenRouter key too."

	msgGenericFail = "Something went wrong on my side. Please try again."
)

// commandMenu is registered with Telegram so commands appear in the app.
var commandMenu = []telegram.Command{
	{Name: "status", Description: "How I'm doing and what's scheduled"},
	{Name: "settings", Description: "How often and when I check in"},
	{Name: "export", Description: "Get a copy of your data"},
	{Name: "forget", Description: "Delete our conversation history"},
	{Name: "lockdown", Description: "Stop writing and unprompted messages"},
	{Name: "help", Description: "What I can do"},
}

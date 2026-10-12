package telegram

import (
	"context"
	"sync"
)

// Fake is an in-memory Transport for tests of code that talks to Telegram.
// It is safe for concurrent use; read what was sent through the accessor
// methods, which take a consistent snapshot.
type Fake struct {
	mu       sync.Mutex
	handler  Handler
	ready    chan struct{}
	once     sync.Once
	sent     []Reply
	voices   []FakeVoice
	docs     []FakeDoc
	acks     []string
	cleared  []int
	commands []Command
	nextID   int

	// Files maps file IDs to content for Download. Set it before Run.
	Files map[string][]byte
}

// FakeVoice records a voice message sent through a Fake.
type FakeVoice struct {
	ChatID int64
	Audio  []byte
	Ext    string
	Silent bool
}

// FakeDoc records a document sent through a Fake.
type FakeDoc struct {
	ChatID   int64
	Filename string
	Data     []byte
	Caption  string
}

var _ Transport = (*Fake)(nil)

// NewFake returns an empty Fake.
func NewFake() *Fake {
	return &Fake{ready: make(chan struct{}), Files: map[string][]byte{}}
}

// Run registers h and blocks until ctx is cancelled.
func (f *Fake) Run(ctx context.Context, h Handler) error {
	f.mu.Lock()
	f.handler = h
	f.mu.Unlock()
	f.once.Do(func() { close(f.ready) })
	<-ctx.Done()
	return nil
}

// Inject delivers an update to the handler, as if the owner had sent it. It
// waits for Run to have registered the handler.
func (f *Fake) Inject(ctx context.Context, u Update) {
	<-f.ready
	f.mu.Lock()
	h := f.handler
	f.mu.Unlock()
	h(ctx, u)
}

// Send records the reply.
func (f *Fake) Send(_ context.Context, r Reply) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	f.sent = append(f.sent, r)
	return f.nextID, nil
}

// SendVoice records the voice message.
func (f *Fake) SendVoice(_ context.Context, chatID int64, audio []byte, ext string, silent bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.voices = append(f.voices, FakeVoice{ChatID: chatID, Audio: audio, Ext: ext, Silent: silent})
	return nil
}

// SendDocument records the document.
func (f *Fake) SendDocument(_ context.Context, chatID int64, filename string, data []byte, caption string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.docs = append(f.docs, FakeDoc{ChatID: chatID, Filename: filename, Data: data, Caption: caption})
	return nil
}

// Typing does nothing.
func (f *Fake) Typing(context.Context, int64, bool) error { return nil }

// Download returns the bytes registered in Files.
func (f *Fake) Download(_ context.Context, fileID string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if b, ok := f.Files[fileID]; ok {
		return b, nil
	}
	return nil, ErrFileNotFound
}

// AnswerCallback records the acknowledgement text.
func (f *Fake) AnswerCallback(_ context.Context, _, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acks = append(f.acks, text)
	return nil
}

// ClearButtons records the message whose buttons were cleared.
func (f *Fake) ClearButtons(_ context.Context, _ int64, messageID int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cleared = append(f.cleared, messageID)
	return nil
}

// SetCommands records the command menu.
func (f *Fake) SetCommands(_ context.Context, cmds []Command) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = cmds
	return nil
}

// Snapshot returns the replies sent so far.
func (f *Fake) Snapshot() []Reply {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Reply(nil), f.sent...)
}

// Voices returns the voice messages sent so far.
func (f *Fake) Voices() []FakeVoice {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]FakeVoice(nil), f.voices...)
}

// Documents returns the documents sent so far.
func (f *Fake) Documents() []FakeDoc {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]FakeDoc(nil), f.docs...)
}

// Cleared returns the message IDs whose buttons were cleared.
func (f *Fake) Cleared() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.cleared...)
}

// Commands returns the registered command menu.
func (f *Fake) Commands() []Command {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Command(nil), f.commands...)
}

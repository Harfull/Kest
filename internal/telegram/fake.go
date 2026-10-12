package telegram

import (
	"context"
	"sync"
)

// Fake is an in-memory Transport for tests of code that talks to Telegram.
// It is safe for concurrent use.
type Fake struct {
	mu       sync.Mutex
	handler  Handler
	ready    chan struct{}
	once     sync.Once
	Sent     []Reply
	Voices   []FakeVoice
	Acks     []string
	Cleared  []int
	Typings  int
	Commands []Command
	Files    map[string][]byte
	nextID   int
}

// FakeVoice records a voice message sent through a Fake.
type FakeVoice struct {
	ChatID int64
	Audio  []byte
	Silent bool
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
	f.Sent = append(f.Sent, r)
	return f.nextID, nil
}

// SendVoice records the voice message.
func (f *Fake) SendVoice(_ context.Context, chatID int64, mp3 []byte, silent bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Voices = append(f.Voices, FakeVoice{ChatID: chatID, Audio: mp3, Silent: silent})
	return nil
}

// Typing counts chat actions.
func (f *Fake) Typing(context.Context, int64, bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Typings++
	return nil
}

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
	f.Acks = append(f.Acks, text)
	return nil
}

// ClearButtons records the message whose buttons were cleared.
func (f *Fake) ClearButtons(_ context.Context, _ int64, messageID int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Cleared = append(f.Cleared, messageID)
	return nil
}

// SetCommands records the command menu.
func (f *Fake) SetCommands(_ context.Context, cmds []Command) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Commands = cmds
	return nil
}

// Snapshot returns a copy of the sent replies.
func (f *Fake) Snapshot() []Reply {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Reply(nil), f.Sent...)
}

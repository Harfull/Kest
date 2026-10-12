package app

import (
	"context"
	"errors"
	"testing"

	"github.com/Harfull/Kest/internal/agent"
	"github.com/Harfull/Kest/internal/plugin"
	"github.com/Harfull/Kest/internal/telegram"
)

type failingPhotoTransport struct{ *telegram.Fake }

func (f failingPhotoTransport) SendPhoto(context.Context, int64, []byte, string) error {
	return errors.New("photo delivery unavailable")
}

func TestPluginPhotosGoOnlyToOwnerAndRespectLockdown(t *testing.T) {
	f := telegram.NewFake()
	c := &chat{tr: f, ownerChat: 42, agent: agent.New(agent.Options{})}
	if err := c.notify(context.Background(), plugin.Notification{Text: "Page", Photo: []byte("fixture")}); err != nil {
		t.Fatal(err)
	}
	if photos := f.Photos(); len(photos) != 1 || photos[0].ChatID != 42 || photos[0].Caption != "Page" {
		t.Fatalf("photos = %+v", photos)
	}
	c.agent.SetLockdown(true)
	if err := c.notify(context.Background(), plugin.Notification{Photo: []byte("fixture")}); err == nil {
		t.Fatal("lockdown photo falsely reported success")
	}
	if len(f.Photos()) != 1 {
		t.Fatal("photo sent during lockdown")
	}
}

func TestPluginPhotoDeliveryFailureIsReturned(t *testing.T) {
	c := &chat{tr: failingPhotoTransport{telegram.NewFake()}, ownerChat: 42, agent: agent.New(agent.Options{})}
	if err := c.notify(context.Background(), plugin.Notification{Photo: []byte("fixture")}); err == nil {
		t.Fatal("Telegram failure was hidden")
	}
}

package telegram

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"strings"
	"testing"
)

func imageBytes(t *testing.T, width, height int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, width, height))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestSendPhotoUploadsBytesAndCaption(t *testing.T) {
	f := newFakeAPI(t)
	b := newBot(t, f)
	if err := b.SendPhoto(context.Background(), 42, imageBytes(t, 20, 20), "Current page"); err != nil {
		t.Fatal(err)
	}
	calls := f.bodies("sendPhoto")
	if len(calls) != 1 || !strings.Contains(calls[0], "Current page") || !strings.Contains(calls[0], "42") {
		t.Fatalf("photo calls = %v", calls)
	}
}

func TestSendPhotoFallsBackToDocumentForTallImages(t *testing.T) {
	f := newFakeAPI(t)
	b := newBot(t, f)
	if err := b.SendPhoto(context.Background(), 42, imageBytes(t, 10, 300), "Long page"); err != nil {
		t.Fatal(err)
	}
	if len(f.bodies("sendPhoto")) != 0 || len(f.bodies("sendDocument")) != 1 {
		t.Fatal("tall image did not use document delivery")
	}
}

func TestSendPhotoRefusesInvalidPayloads(t *testing.T) {
	f := newFakeAPI(t)
	b := newBot(t, f)
	for _, tc := range []struct {
		name    string
		data    []byte
		caption string
	}{
		{"empty", nil, ""}, {"not image", []byte("secret text"), ""},
		{"oversize", make([]byte, maxPhotoBytes+1), ""},
		{"long caption", imageBytes(t, 2, 2), strings.Repeat("x", 1025)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := b.SendPhoto(context.Background(), 42, tc.data, tc.caption); err == nil {
				t.Fatal("invalid photo accepted")
			}
		})
	}
	if len(f.bodies("sendPhoto")) != 0 {
		t.Fatal("invalid image sent to Telegram")
	}
}

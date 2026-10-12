package telegram

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // register safe image headers for validation
	_ "image/png"
	"unicode/utf8"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// Photo bounds follow Telegram's sendPhoto API: 10 MB, dimension sum 10000,
// aspect ratio 20, and caption 1024 characters. Large dimensions use a document.
const maxPhotoBytes = 10 << 20

// SendPhoto sends image bytes, never a URL or a host filesystem path.
func (t *Bot) SendPhoto(ctx context.Context, chatID int64, data []byte, caption string) error {
	if len(data) == 0 || len(data) > maxPhotoBytes || utf8.RuneCountInString(caption) > 1024 {
		return errors.New("telegram: photo or caption exceeds limits")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "png" && format != "jpeg") || cfg.Width <= 0 || cfg.Height <= 0 {
		return errors.New("telegram: invalid PNG or JPEG photo")
	}
	if cfg.Width+cfg.Height > 10000 || cfg.Width > 20*cfg.Height || cfg.Height > 20*cfg.Width {
		ext := "png"
		if format == "jpeg" {
			ext = "jpg"
		}
		return t.SendDocument(ctx, chatID, "screenshot."+ext, data, caption)
	}
	_, err = t.b.SendPhoto(ctx, &tgbot.SendPhotoParams{ChatID: chatID, Photo: &models.InputFileUpload{Filename: "screenshot." + format, Data: bytes.NewReader(data)}, Caption: caption})
	if err != nil {
		return fmt.Errorf("telegram: send photo: %w", scrub(err, t.token))
	}
	return nil
}

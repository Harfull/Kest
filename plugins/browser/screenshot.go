package browser

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Harfull/Kest/internal/plugin"
)

// maxImageBytes bounds a screenshot read back from the worker's directory.
const maxImageBytes = 10 << 20

// readScreenshot loads the PNG the worker wrote and deletes it: a screenshot
// is a message, not an archive, and pages can show private information.
func (p *Plugin) readScreenshot(path string) ([]byte, error) {
	dir := filepath.Join(p.dir, "screenshots")
	rel, err := filepath.Rel(dir, path)
	if err != nil || filepath.Dir(rel) != "." || filepath.Ext(rel) != ".png" {
		return nil, errors.New("screenshot path is outside browser artifacts")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open screenshot directory: %w", err)
	}
	defer root.Close() //nolint:errcheck // confined directory handle
	defer func() { _ = root.Remove(rel) }()
	f, err := root.Open(rel)
	if err != nil {
		return nil, fmt.Errorf("open screenshot artifact: %w", err)
	}
	defer f.Close() //nolint:errcheck // read-only image
	// Bound the read even if a local file grows after the worker captures it.
	data, err := io.ReadAll(io.LimitReader(f, maxImageBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read screenshot artifact: %w", err)
	}
	if len(data) == 0 || len(data) > maxImageBytes {
		return nil, errors.New("screenshot exceeds the 10 MB image limit")
	}
	if _, err := png.DecodeConfig(bytes.NewReader(data)); err != nil {
		return nil, errors.New("screenshot is not a valid PNG")
	}
	return data, nil
}

// toJPEG re-encodes a screenshot for the model: the pixels are the same to a
// reader of text and charts, and the payload is several times smaller.
func toJPEG(data []byte) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode screenshot: %w", err)
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: 80}); err != nil {
		return nil, fmt.Errorf("encode screenshot: %w", err)
	}
	return out.Bytes(), nil
}

// allowShot enforces screenshotsPerMinute. The caller holds the gate.
func (p *Plugin) allowShot() bool {
	now := p.host.Clock().Now()
	kept := p.shots[:0]
	for _, t := range p.shots {
		if now.Sub(t) < time.Minute {
			kept = append(kept, t)
		}
	}
	p.shots = kept
	if len(p.shots) >= screenshotsPerMinute {
		return false
	}
	p.shots = append(p.shots, now)
	return true
}

// finishScreenshot delivers a captured image where the call asked: the owner's
// chat, the model, or both. It reports honestly which of them happened.
func (p *Plugin) finishScreenshot(ctx context.Context, args arguments, r result, res plugin.Result) (plugin.Result, error) {
	data, err := p.readScreenshot(r.Screenshot)
	if err != nil {
		return plugin.Result{}, err
	}
	what := "the visible screen"
	switch {
	case args.Ref != "" || args.Selector != "":
		what = "one element"
	case args.FullPage:
		what = "the whole page"
	}
	var out []string
	out = append(out, fmt.Sprintf("Screenshot of %s: %s (%s).", what, clip(plain(r.Title), 100), clip(plain(r.URL), 200)))
	delivered := false

	if args.Send {
		switch {
		case !p.allowShot():
			out = append(out, "Not sent to Telegram: too many screenshots in the last minute. Wait before sending another.")
		default:
			if err := p.host.Notify(ctx, plugin.Notification{Text: args.Caption, Photo: data}); err != nil {
				p.host.Logger().WarnContext(ctx, "send browser screenshot", "err", err)
				out = append(out, "Not sent to Telegram: delivery failed. Tell the owner it was not sent.")
			} else {
				out = append(out, "Sent to the owner in Telegram.")
				delivered = true
			}
		}
	}
	if args.Look {
		jpg, err := toJPEG(data)
		if err != nil {
			return plugin.Result{}, err
		}
		res.Image = &plugin.Image{MediaType: "image/jpeg", Data: jpg}
		out = append(out, "The image is attached below for you to look at.")
		delivered = true
	}
	if !delivered {
		return plugin.Result{}, errors.New(strings.Join(out, " "))
	}
	for _, n := range r.Notes {
		out = append(out, "Note: "+plain(n))
	}
	res.Content = strings.Join(out, "\n")
	return res, nil
}

package agent

import (
	"context"
	"errors"

	"github.com/Harfull/Kest/internal/llm"
	"github.com/Harfull/Kest/internal/plugin"
)

type imagesKey struct{}

// turnImages is owned by the serial loop. It never persists image bytes.
type turnImages struct {
	pending []llm.Image
	count   int
}

// collectImage queues a tool's image for the model. The pixels go to the same
// model provider that already receives the tool's text, so no separate approval
// is needed; they are untrusted like the rest of the result.
func collectImage(ctx context.Context, im *plugin.Image) (bool, error) {
	if (im.MediaType != "image/png" && im.MediaType != "image/jpeg") || len(im.Data) == 0 || len(im.Data) > 10<<20 {
		return false, errors.New("invalid or oversized tool image")
	}
	images, _ := ctx.Value(imagesKey{}).(*turnImages)
	// Two images bound prompt size while permitting before/after inspection.
	if images == nil || images.count >= 2 {
		return false, nil
	}
	images.pending = append(images.pending, llm.Image{MediaType: im.MediaType, Data: im.Data})
	images.count++
	return true, nil
}

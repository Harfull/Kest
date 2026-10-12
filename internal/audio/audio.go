// Package audio converts the raw speech some text-to-speech models return into a
// format Telegram plays as a voice message.
//
// Telegram accepts voice messages as Ogg/Opus, MP3 or M4A. Some models, such as
// Gemini TTS, return only headerless 16-bit PCM, so Kest encodes it to Ogg/Opus
// itself with a pure-Go encoder: no ffmpeg, no cgo, nothing extra to install on
// the host.
package audio

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/container/ogg"
)

const (
	// DefaultPCMRate is the sample rate speech models document for PCM output:
	// OpenAI, Gemini and Azure all produce 24 kHz.
	DefaultPCMRate = 24000

	// bitrate is plenty for speech and keeps voice notes small.
	bitrate = 32000
	// maxSeconds bounds how much audio is encoded. A 4096-character reply is
	// about five minutes of speech.
	maxSeconds = 10 * 60
)

// supportedRates are the sample rates the Opus encoder accepts directly.
var supportedRates = map[int]bool{8000: true, 12000: true, 16000: true, 24000: true, 48000: true}

// PCMToOggOpus encodes 16-bit little-endian signed PCM as a mono Ogg/Opus file.
// channels may be 1 or 2; stereo is mixed down to mono, which is right for
// speech. Sample rates the encoder does not accept are resampled to 24 kHz.
func PCMToOggOpus(pcm []byte, rate, channels int) ([]byte, error) {
	if channels != 1 && channels != 2 {
		return nil, fmt.Errorf("audio: %d channels not supported (want 1 or 2)", channels)
	}
	if rate < 4000 || rate > 192000 {
		return nil, fmt.Errorf("audio: implausible sample rate %d", rate)
	}
	frameBytes := 2 * channels
	pcm = pcm[:len(pcm)/frameBytes*frameBytes] // drop a trailing partial sample
	if len(pcm) == 0 {
		return nil, errors.New("audio: no audio to encode")
	}
	if len(pcm)/frameBytes > rate*maxSeconds {
		return nil, fmt.Errorf("audio: longer than %d minutes", maxSeconds/60)
	}

	samples := mono(pcm, channels)
	if !supportedRates[rate] {
		samples = resample(samples, rate, DefaultPCMRate)
		rate = DefaultPCMRate
	}
	return encode(samples, rate)
}

// mono converts interleaved little-endian PCM to mono samples, averaging channels.
func mono(pcm []byte, channels int) []int16 {
	n := len(pcm) / (2 * channels)
	out := make([]int16, n)
	for i := range n {
		sum := 0
		for c := range channels {
			o := (i*channels + c) * 2
			v := int(pcm[o]) | int(pcm[o+1])<<8 // little-endian, then sign-extend
			if v >= 1<<15 {
				v -= 1 << 16
			}
			sum += v
		}
		out[i] = int16(sum / channels) //nolint:gosec // an average of int16 values fits in int16
	}
	return out
}

// resample converts between sample rates by linear interpolation, which is
// adequate for speech on its way into a lossy codec.
func resample(in []int16, from, to int) []int16 {
	n := int(int64(len(in)) * int64(to) / int64(from))
	if n == 0 {
		return in
	}
	out := make([]int16, n)
	step := float64(from) / float64(to)
	for i := range out {
		pos := float64(i) * step
		j := int(pos)
		frac := pos - float64(j)
		a := float64(in[min(j, len(in)-1)])
		b := float64(in[min(j+1, len(in)-1)])
		out[i] = int16(a + (b-a)*frac)
	}
	return out
}

func encode(samples []int16, rate int) ([]byte, error) {
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: rate, Channels: 1, Application: gopus.ApplicationVoIP})
	if err != nil {
		return nil, fmt.Errorf("audio: create encoder: %w", err)
	}
	if err := enc.SetBitrate(bitrate); err != nil {
		return nil, fmt.Errorf("audio: set bitrate: %w", err)
	}

	frame := rate / 50 // 20 ms
	if err := enc.SetFrameSize(frame); err != nil {
		return nil, fmt.Errorf("audio: set frame size: %w", err)
	}

	// Ogg Opus counts everything in 48 kHz units, whatever the input rate.
	to48 := func(n int) int { return int(int64(n) * 48000 / int64(rate)) }

	var buf bytes.Buffer
	w, err := ogg.NewWriterWithConfig(&buf, ogg.WriterConfig{
		SampleRate: uint32(rate), //nolint:gosec // rate is checked to be 4000-192000
		Channels:   1,
		PreSkip:    uint16(to48(enc.Lookahead())), //nolint:gosec // lookahead is a few hundred samples
	})
	if err != nil {
		return nil, fmt.Errorf("audio: create ogg writer: %w", err)
	}

	packet := make([]byte, 1500)
	total := len(samples)
	for off := 0; off < total; off += frame {
		chunk := samples[off:min(off+frame, total)]
		if len(chunk) < frame { // pad the last frame with silence
			padded := make([]int16, frame)
			copy(padded, chunk)
			chunk = padded
		}
		n, err := enc.EncodeInt16(chunk, packet)
		if err != nil {
			return nil, fmt.Errorf("audio: encode: %w", err)
		}
		if n == 0 {
			continue
		}
		last := off+frame >= total
		if last {
			// Count only the real samples, not the padding, in the final position.
			err = w.WriteFinalPacket(packet[:n], to48(total-off))
		} else {
			err = w.WritePacket(packet[:n], to48(frame))
		}
		if err != nil {
			return nil, fmt.Errorf("audio: write ogg: %w", err)
		}
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("audio: finish ogg: %w", err)
	}
	return buf.Bytes(), nil
}

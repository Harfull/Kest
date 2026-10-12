package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const (
	// maxTranscribeBytes is OpenRouter's inline audio cap for most providers.
	maxTranscribeBytes = 25 << 20
	// maxSpeechChars keeps spoken replies short and cheap. Longer text should
	// be sent as a normal message.
	maxSpeechChars = 4096
)

// ErrAudioTooLarge is returned when audio exceeds the upload cap.
var ErrAudioTooLarge = errors.New("llm: audio exceeds the 25 MB limit")

// ErrTextTooLong is returned when text is too long to synthesize.
var ErrTextTooLong = fmt.Errorf("llm: text exceeds %d characters for speech", maxSpeechChars)

// Transcribe converts audio to text with the transcription model.
//
// format is the audio container or codec name, for example "ogg" for Telegram
// voice notes. language is an optional ISO-639-1 hint; empty auto-detects.
func (c *Client) Transcribe(ctx context.Context, audio []byte, format, language string) (string, error) {
	if len(audio) == 0 {
		return "", errors.New("llm: no audio to transcribe")
	}
	if len(audio) > maxTranscribeBytes {
		return "", ErrAudioTooLarge
	}
	if c.o.Transcription == "" {
		return "", errors.New("llm: no transcription model configured")
	}

	payload := map[string]any{
		"model": c.o.Transcription,
		"input_audio": map[string]string{
			"data":   base64.StdEncoding.EncodeToString(audio), // raw base64, not a data URI
			"format": format,
		},
	}
	if language != "" {
		payload["language"] = language
	}
	if c.o.ZDROnly {
		payload["provider"] = map[string]any{"zdr": true}
	}

	body, header, err := c.do(ctx, "/audio/transcriptions", payload)
	if err != nil {
		return "", err
	}

	var resp struct {
		Text  string `json:"text"`
		Usage struct {
			InputTokens  int     `json:"input_tokens"`
			OutputTokens int     `json:"output_tokens"`
			Cost         float64 `json:"cost"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("decode transcription response: %w", err)
	}

	c.record(ctx, "transcription", c.o.Transcription, Usage{
		PromptTokens:     resp.Usage.InputTokens,
		CompletionTokens: resp.Usage.OutputTokens,
		Cost:             resp.Usage.Cost,
	}, header.Get("X-Generation-Id"))

	return strings.TrimSpace(resp.Text), nil
}

// Speech is synthesized audio as the model returned it.
type Speech struct {
	Data []byte
	// Format is "mp3" (ready to send) or "pcm" (headerless 16-bit little-endian
	// signed samples that must be encoded before Telegram can play them).
	Format string
	// SampleRate and Channels describe PCM data; they are zero for MP3.
	SampleRate int
	Channels   int
}

// Speech formats OpenRouter's speech endpoint can produce.
const (
	FormatMP3 = "mp3"
	FormatPCM = "pcm"
)

// defaultPCMRate is what speech models document for PCM output (OpenAI, Gemini
// and Azure all produce 24 kHz), used when the response does not say.
const defaultPCMRate = 24000

var (
	// supportsFormatRe finds the format a model says it requires, as in
	// 'Gemini TTS only supports response_format="pcm". Got "mp3".'
	supportsFormatRe = regexp.MustCompile(`(?i)(?:only )?supports?\s+(?:response_format|format)s?\W{0,3}(mp3|pcm)`)
	mentionsFormatRe = regexp.MustCompile(`(?i)response_format|audio format|output format`)
	mentionsVoiceRe  = regexp.MustCompile(`(?i)\bvoice\b`)
)

// Speak converts text to speech with the TTS model.
//
// Models differ in what they can return: OpenAI and Voxtral give MP3, Gemini
// gives only PCM. Speak asks for MP3, and if the model rejects that format it
// reads the error, switches to the format the model asked for, and remembers the
// choice for that model so later calls go straight to it. The caller converts
// PCM with the audio package.
//
// The speech endpoint returns raw audio without a usage object, so cost is
// recorded as zero with the generation ID for later lookup.
func (c *Client) Speak(ctx context.Context, text string) (*Speech, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("llm: no text to speak")
	}
	if len(text) > maxSpeechChars {
		return nil, ErrTextTooLong
	}
	if c.o.TTS == "" {
		return nil, errors.New("llm: no text-to-speech model configured")
	}

	format := FormatMP3
	if f, ok := c.ttsFormat.Load(c.o.TTS); ok {
		format = f.(string)
	}
	tried := map[string]bool{}

	// Voices differ per model, and some providers insist on one. Try the configured
	// voice, then the built-in default for the model, then none; start from the
	// one that worked last time.
	voices := voiceCandidates(c.o.TTS, c.o.TTSVoice)
	vi := 0
	if v, ok := c.ttsVoice.Load(c.o.TTS); ok {
		if i := slices.Index(voices, v.(string)); i >= 0 {
			vi = i
		}
	}

	for {
		tried[format] = true
		payload := map[string]any{"model": c.o.TTS, "input": text, "response_format": format}
		if voices[vi] != "" {
			payload["voice"] = voices[vi]
		}
		body, header, err := c.do(ctx, "/audio/speech", payload)
		if err == nil {
			if len(body) == 0 {
				return nil, errors.New("llm: speech response was empty")
			}
			sp, err := speechFrom(body, header, format)
			if err != nil {
				return nil, err
			}
			c.ttsFormat.Store(c.o.TTS, sp.Format)
			c.ttsVoice.Store(c.o.TTS, voices[vi])
			c.record(ctx, "tts", c.o.TTS, Usage{}, header.Get("X-Generation-Id"))
			return sp, nil
		}

		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.Status != http.StatusBadRequest {
			return nil, err
		}
		if next := alternativeFormat(apiErr.Message, tried); next != "" {
			c.o.Logf(ctx, "text-to-speech model rejected a format; switching",
				"model", c.o.TTS, "from", format, "to", next, "reason", apiErr.Message)
			format = next
			continue
		}
		if mentionsVoiceRe.MatchString(apiErr.Message) && vi < len(voices)-1 {
			c.o.Logf(ctx, "text-to-speech model rejected the voice; trying the next",
				"model", c.o.TTS, "voice", voices[vi], "reason", apiErr.Message)
			vi++
			continue
		}
		if mentionsVoiceRe.MatchString(apiErr.Message) {
			return nil, fmt.Errorf("%w (the voice %q may not exist for this model; voices differ per model, so set KEST_TTS_VOICE to one listed on the model's page)",
				err, c.o.TTSVoice)
		}
		return nil, err
	}
}

// builtinVoice returns a voice known to work for a speech model family, or "".
// Verified against OpenRouter in October 2026; voices differ per model, so this
// is only a safety net for when the configured voice does not fit the model.
func builtinVoice(model string) string {
	switch {
	case strings.HasPrefix(model, "google/"):
		return "Kore"
	case strings.HasPrefix(model, "hexgrad/"):
		return "af_heart"
	case strings.HasPrefix(model, "mistralai/"):
		return "en_paul_neutral"
	case strings.HasPrefix(model, "openai/"):
		return "alloy"
	}
	return ""
}

// voiceCandidates lists the voices to try in order, without repeats. The empty
// string means sending no voice and ends the list.
func voiceCandidates(model, configured string) []string {
	var out []string
	for _, v := range []string{configured, builtinVoice(model)} {
		if v != "" && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return append(out, "")
}

// alternativeFormat picks the format to try after a 400 about response_format:
// the one the error asks for, or else the other one we have not tried. It returns
// "" when the error is not about the format or nothing is left to try.
func alternativeFormat(message string, tried map[string]bool) string {
	if m := supportsFormatRe.FindStringSubmatch(message); m != nil {
		if f := strings.ToLower(m[1]); !tried[f] {
			return f
		}
		return ""
	}
	if !mentionsFormatRe.MatchString(message) {
		return ""
	}
	for _, f := range []string{FormatMP3, FormatPCM} {
		if !tried[f] {
			return f
		}
	}
	return ""
}

// speechFrom classifies a speech response. The Content-Type is trusted over the
// requested format, because some providers answer in their own format.
func speechFrom(body []byte, header http.Header, requested string) (*Speech, error) {
	ct, params, _ := mime.ParseMediaType(header.Get("Content-Type"))
	switch {
	case strings.Contains(ct, "mpeg") || strings.Contains(ct, "mp3"):
		return &Speech{Data: body, Format: FormatMP3}, nil
	case strings.Contains(ct, "pcm") || strings.Contains(ct, "l16") || (ct == "" && requested == FormatPCM):
		rate, channels := defaultPCMRate, 1
		if v, err := strconv.Atoi(params["rate"]); err == nil && v > 0 {
			rate = v
		}
		if v, err := strconv.Atoi(params["channels"]); err == nil && v > 0 {
			channels = v
		}
		return &Speech{Data: body, Format: FormatPCM, SampleRate: rate, Channels: channels}, nil
	case ct == "" && requested == FormatMP3:
		return &Speech{Data: body, Format: FormatMP3}, nil
	}
	return nil, fmt.Errorf("llm: speech response has unsupported content type %q", ct)
}

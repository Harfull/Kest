package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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

// Speak converts text to MP3 audio with the TTS model. Telegram's sendVoice
// accepts MP3, so no transcoding is needed.
//
// The speech endpoint returns raw audio without a usage object, so cost is
// recorded as zero with the generation ID for later lookup.
func (c *Client) Speak(ctx context.Context, text string) ([]byte, error) {
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

	payload := map[string]any{
		"model":           c.o.TTS,
		"input":           text,
		"voice":           c.o.TTSVoice,
		"response_format": "mp3",
	}

	audio, header, err := c.do(ctx, "/audio/speech", payload)
	if err != nil {
		return nil, err
	}
	if len(audio) == 0 {
		return nil, errors.New("llm: speech response was empty")
	}

	c.record(ctx, "tts", c.o.TTS, Usage{}, header.Get("X-Generation-Id"))
	return audio, nil
}

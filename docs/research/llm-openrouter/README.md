# LLM stack: OpenRouter

## Question

How should Kest use OpenRouter for its three model roles (general, transcription, text-to-speech), including tool calling, fallbacks and cost tracking?

## Sources

- Primary: [OpenRouter API overview](https://openrouter.ai/docs/api-reference/overview)
- Primary: [Tool calling](https://openrouter.ai/docs/guides/features/tool-calling)
- Primary: [Speech-to-text reference](https://openrouter.ai/docs/guides/overview/multimodal/stt)
- Primary: [Audio APIs announcement](https://openrouter.ai/blog/announcements/announcing-audio-apis)

## Findings

### Chat completions

- Base URL `https://openrouter.ai/api/v1`, endpoint `POST /chat/completions`, auth `Authorization: Bearer <key>`.
- Optional attribution headers: `HTTP-Referer` and `X-OpenRouter-Title` (`X-Title` also accepted).
- The request shape is OpenAI-compatible. `models` (array) plus `route: "fallback"` enables model fallback; omitting `model` uses the account default.
- Finish reasons are normalized to `tool_calls`, `stop`, `length`, `content_filter`, `error`. The provider's raw value is in `native_finish_reason`.
- Non-streaming responses always include `usage` (`prompt_tokens`, `completion_tokens`, `total_tokens`, and `cost`). Streaming sends usage in the final chunk. `GET /generation?id=` returns stats after the fact.
- Errors use an object with `code`, `message` and optional `metadata`. Rate-limit specifics are not documented on the overview page, so the client must treat 429 and 5xx as retryable with backoff.

### Tool calling

- The model never executes tools. It returns `tool_calls`; we run them and send back `role: "tool"` messages with the matching `tool_call_id`.
- Loop: call model, append the assistant message, stop if there are no `tool_calls`, otherwise run each tool, append results, repeat with a hard iteration cap.
- The `tools` array must be sent on every request in the loop.
- The assistant message containing `tool_calls` must be appended **before** the tool results.
- `arguments` is a JSON **string**; parse it ourselves. Tool result `content` must be a string.
- `parallel_tool_calls` defaults to true on most models; we can set it false for confirmation-gated flows.
- `tool_choice`: `auto`, `none`, or a forced function.
- Streaming tool calls arrive in fragments and must be accumulated.
- Only some models support tools. The general model must be chosen from tool-capable models.

### Transcription

- `POST /audio/transcriptions`. JSON body: `model`, `input_audio.data` (raw base64, not a data URI), `input_audio.format` (wav, mp3, flac, m4a, ogg, webm, aac), optional `language`, `temperature`, `response_format` (`json` or `verbose_json`).
- Response: `{ "text": "...", "usage": { "seconds", "total_tokens", "cost", ... } }`. `verbose_json` adds language, duration, segments, words (provider dependent).
- Limits: 25 MB multipart cap; larger base64 only for OpenAI and Groq models; 60 second upstream timeout, so split long recordings.
- Telegram voice notes are OGG/Opus, and OGG is a supported input format, so no transcoding is needed.
- Listed models: `openai/whisper-large-v3`, `openai/gpt-4o-transcribe`, `openai/gpt-4o-mini-transcribe`, `google/chirp-3`. `openai/gpt-4o-transcribe` rejects `verbose_json`. Discover live models with `output_modalities=transcription`.
- Routing options such as `order` and `allow_fallbacks` do not apply to this endpoint, so transcription fallback must be implemented client-side.

### Text to speech

- `POST /audio/speech`, JSON body: `model`, `input`, `voice`, `response_format` (`mp3` or `pcm`), and provider-specific fields such as `instructions` for OpenAI models.
- The response is a raw audio byte stream.
- Listed models: `openai/gpt-4o-mini-tts-2025-12-15`, `google/gemini-3.1-flash-tts-preview`, `mistralai/voxtral-mini-tts-2603`. Voices are per model.
- Telegram `sendVoice` accepts MP3, so no ffmpeg is needed. See [telegram](../telegram/README.md).
- Model slugs change often. They must be configuration, never constants.

## Decision

1. Write a **thin, in-house OpenRouter client** on `net/http` in `internal/llm`. The surface is small (chat, tools, two audio calls), and an SDK would hide retry, cost and fallback behavior we want to control.
2. Three configured **model slots**: `general`, `transcription`, `tts`. `general` accepts an ordered fallback list sent as `models`. Transcription and TTS fallbacks are handled client-side.
3. Wrap every call with: timeout, bounded retry with jittered backoff on 429/5xx, context cancellation, and cost recording from `usage` into the store.
4. Agent loop in `internal/agent` caps iterations (default 8) and tool calls per turn, and supports confirmation interrupts.
5. A `KEST_DAILY_BUDGET_USD` guard stops proactive messages and expensive calls when exceeded.
6. Never log prompts or completions at default log level.

## Open questions

- Which general model gives the best tone for the persona at acceptable cost? Run a small evaluation harness (`docs/guides/model-evaluation.md`, Phase 2) with fixed persona prompts.
- Does streaming into Telegram (`sendMessageDraft`, Bot API 10.x) feel better than one final message? Evaluate in Phase 2.
- Exact rate limits are undocumented; observe in production and tune the client.

# 0005. Thin in-house OpenRouter client with three model slots

- Status: Accepted
- Date: 2026-10-11

## Context

OpenRouter exposes an OpenAI-compatible chat API with tool calling, plus `/audio/transcriptions` and `/audio/speech`. Model slugs change often. See [research](../research/llm-openrouter/README.md).

## Decision

- Implement `internal/llm` directly on `net/http`.
- Three configurable model slots: `general`, `transcription`, `tts`. `general` accepts ordered fallbacks.
- Record usage and cost for every call and enforce a configurable daily budget.

## Consequences

- Full control of retries, fallbacks, cost and logging; no SDK lock-in.
- We maintain a small amount of API-shape code and its tests.

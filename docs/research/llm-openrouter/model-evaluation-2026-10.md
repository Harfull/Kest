# General model evaluation, October 2026

## Question

Which cheap, current model should be Kest's default `general` model, and which should back it up as a fallback?

## Sources

- The harness in `internal/agent/eval_test.go` (see [the evaluation guide](../../guides/model-evaluation.md)), run on 2026-10-12 against the live OpenRouter API.
- OpenRouter's model list filtered by tool support, for prices and the latest model per provider, read on the same day: `GET /api/v1/models?supported_parameters=tools`.

## Method

Each model ran the full harness twice: five tone prompts checked against `voice.Check`, one reminder tool call that must carry an absolute local time, and one prompt-injection page that tries to make the model email the owner's data. Prices are USD per million tokens, input and output. The suite time is wall clock for all eight checks, so it is a rough latency signal only.

## Findings

| Model | Price (in / out) | Run 1 | Run 2 | Suite time | Notes |
| --- | --- | --- | --- | --- | --- |
| `anthropic/claude-haiku-5.5` | 0.10 / 0.50 | all pass | all pass | 18 to 22 s | Short replies, one question at a time. Also passed an earlier run. |
| `deepseek/deepseek-v4.1-flash` | 0.30 / 1.20 | all pass | all pass | 17 to 18 s | Warm, slightly longer replies. |
| `google/gemini-3.8-flash` | 0.38 / 1.88 | all pass | all pass | 30 to 34 s | Slowest of the group. Named the injection attempt to the owner. |
| `openai/gpt-6-luna` | 0.05 / 0.25 | all pass | 1 tone failure | 23 to 25 s | Cheapest. Used "you need to" once, which the voice guide bans. |

Earlier, `openai/gpt-5.6-luna` also failed the same tone rule once, so the Luna family leans toward directive phrasing on the sleep prompt.

Every model passed the reminder check and none sent anything in the injection check. That check cannot fail on the model alone, because the tool policy blocks outbound sends; see the evaluation guide.

## Decision

- **Default `general`: `anthropic/claude-haiku-5.5`.** It passed every check in every run and is the cheapest of the models that did.
- **Suggested fallback: `deepseek/deepseek-v4.1-flash`**, set with `KEST_MODEL_FALLBACKS`. It is a different provider, so an outage at one does not take out both. It is not a default, because a fallback costs nothing until it is needed and the owner may prefer another.
- **Not recommended yet: `openai/gpt-6-luna`.** It is half the price of Haiku, but one failure in two runs on tone matters for a product whose point is a calm, steady voice. Revisit with a larger sample or a prompt tweak.

## Open questions

- Two runs per model is a small sample. Failures here are rare events, so a model that passed twice may still slip occasionally. Re-run before trusting a cheaper model.
- The harness checks tone on five prompts, one tool and one injection page. It does not measure long-conversation drift, multi-tool turns or cost per day. Add scenarios as the coach and integrations arrive.
- Prices and model ids change often. This is a snapshot, not a recommendation to keep forever.

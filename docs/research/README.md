# Research

Everything learned while designing Kest, one folder per topic. Each document follows the same shape: question, sources, findings, decision, open questions.

Research was done in October 2026. Where a claim came from a secondary source or could not be verified against the primary one, the document says so. Treat anything marked **unverified** as a lead, not a fact.

## Index

| Topic | Question it answers | Outcome |
| --- | --- | --- |
| [llm-openrouter](llm-openrouter/README.md) | How do we use OpenRouter for chat, tools, transcription and speech? | Thin in-house client; three model slots plus fallbacks |
| [telegram](telegram/README.md) | Which library and update mode? What are the platform limits? | `go-telegram/bot`, long polling |
| [hosting](hosting/README.md) | What does a Pterodactyl egg need? | PTDL_v2 egg, release-binary install, env-driven config |
| [integrations](integrations/README.md) | How do we reach Gmail, SMTP, Notion and "hundreds" more? | Native plugins for core services, MCP client for the long tail |
| [storage-scheduling](storage-scheduling/README.md) | Which database and how do we schedule reliably? | SQLite (pure Go), custom persistent scheduler |
| [behavior-science](behavior-science/README.md) | What actually helps people stay consistent? | A short list of evidence-backed techniques and design rules |
| [sleep](sleep/README.md) | How do we estimate sleep without a wearable? | Hybrid: explicit check-ins plus phone-interaction inference with confidence |
| [security](security/README.md) | How do we keep an agent with inbox access safe? | Owner-only, risk-tiered tools, untrusted-data discipline |

## Source quality labels

- **Primary**: official docs or the original paper.
- **Secondary**: summaries, blog posts, aggregator pages.
- **Unverified**: seen only in a low-quality source or contradicted elsewhere.

## Adding research

Create `docs/research/<topic>/README.md` using the template below, link it from this index, and reference it from any ADR or code comment that depends on it.

```markdown
# Topic

## Question
## Sources
## Findings
## Decision
## Open questions
```

## Optional browser research

See [browser/](browser/README.md) for Chromium, Playwright, persistent profiles, Pterodactyl constraints and private desktop viewing.

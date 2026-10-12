# 0010. Coaching domains as plugins

- Status: Accepted
- Date: 2026-10-12

## Context

[The coach engine design](../architecture/coach-engine.md) planned workout, nutrition and focus as built-in modules under `internal/coach/<id>/` behind a `Module` interface (`Handle`, `Prompts`, `Summary`), to arrive with the first domain that needed it. [AGENTS.md](../../AGENTS.md) also says every integration and every behavior module is a plugin behind an interface, and that core never imports a plugin.

When the three domains were built, what each actually needed from the engine was small: to put the owner's current data in front of the model, to add a line to the weekly review, and (for one of them) to run a timer. None needed to decide when the coach speaks.

## Decision

Build workout, nutrition and focus as ordinary plugins (`plugins/workout`, `plugins/nutrition`, `plugins/focus`) that store their records in the plugin key/value store through the shared `plugins/internal/logbook` helper, and give the engine three small, generic hooks instead of a `Module` interface:

- `plugin.ContextProvider`: a plugin adds owner data and guidance to the system prompt each turn.
- `plugin.Digester`: a plugin adds a line to the weekly review.
- Non-urgent `Host.Notify` calls go through the coach's notification budget (`Coach.Nudge`) and return `plugin.ErrNotSent` when held back. This also serves the opt-in email nudges.

Check-ins about these domains stay habits: a workout reminder is a habit with an if-then plan, so the existing prompts, consistency numbers and lapse protocol apply unchanged.

## Consequences

- The coach stays one engine with no module registry, and a new domain (hydration, study) is a plugin with no change to core.
- Plugins do not compose proactive messages of their own beyond what the owner asked for (a focus timer's end), so the budget and quiet hours cannot be bypassed by a new domain.
- Records live in the key/value store, so `/forget` does not delete them (like tasks) and `/export` does not include them until export learns to read plugin storage. Both are noted in the plugin READMEs.
- If a future domain needs to decide *when* the coach speaks, such as a smart morning plan that depends on training load, that is the point to introduce a `Module` interface; this ADR would then be superseded.

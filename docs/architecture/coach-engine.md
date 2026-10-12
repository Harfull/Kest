# Coach engine

The coach is what makes Kest more than a chatbot with tools. It owns habits, the daily rhythm, the sleep model and the decision of **whether, when and how** to speak. Evidence and rationale: [../research/behavior-science/](../research/behavior-science/README.md) and [../research/sleep/](../research/sleep/README.md). Voice rules: [../persona/voice.md](../persona/voice.md).

## Responsibilities

1. Maintain the owner's **habits**, **if-then plans** and **logs**.
2. Compute **consistency** (not streaks).
3. Run the **daily rhythm**: morning, midday, evening and weekly check-ins.
4. Maintain the **sleep model** and **quiet hours**.
5. Decide message **timing and volume** with the **notification budget**.
6. Handle **lapses** with the lapse protocol.
7. Learn what works through bounded **adaptation** and opt-in **experiments**.

## Module interface

Coaching domains are modules so new ones (hydration, study, meditation) can be added without touching the engine.

```go
type Module interface {
    ID() string
    // Handle reacts to a domain event (a logged workout, a skipped meal, a bedtime message).
    Handle(ctx context.Context, ev Event) ([]Intent, error)
    // Prompts returns check-in prompts this module wants at a daily slot.
    Prompts(ctx context.Context, slot Slot) ([]Prompt, error)
    // Summary contributes to the daily/weekly review.
    Summary(ctx context.Context, period Period) (Section, error)
}

// Intent is a request to speak or to change state; coach decides if and when it goes out.
type Intent struct {
    Kind     IntentKind // Message, Question, ScheduleJob, UpdateHabit
    Priority Priority   // Low, Normal, High; High is only for owner-requested reminders
    Payload  any
}
```

Built-in modules: `habit` and `sleep` live in `internal/coach/`. Workout, nutrition and focus were built as plugins instead, with no `Module` interface: see [ADR 0010](../decisions/0010-coaching-domains-as-plugins.md). They reach the engine through three generic hooks: `ContextProvider` (owner data in the system prompt), `Digester` (a line in the weekly review) and the notification budget for non-urgent plugin messages (`Coach.Nudge`).

## Habits and plans

A habit is stored with a structured plan:

| Field | Meaning |
| --- | --- |
| `name` | "Walk after lunch" |
| `anchor` | "After I finish lunch" (a routine) or a clock time |
| `behavior` | "Walk for 10 minutes" |
| `tiny_version` | "Put on shoes and step outside" (the worst-day fallback) |
| `window` | When prompts may fire |
| `cadence` | Daily, weekdays, N times per week |
| `state` | `active`, `paused`, `retired` |

Creating a habit is a short guided conversation: goal, why it matters in the owner's words, anchor, size ("could you do this on your worst day?"), and fallback. The plan is stored as data and reused in prompts.

## Consistency, not streaks

For each habit:

- `rate7` and `rate28`: completed over expected opportunities in the last 7 and 28 days. A tiny-version completion counts as completed.
- `showing_up_days`: total days with any completion, never reset.
- `missed_in_a_row`: used only to trigger the lapse protocol, never shown as a countdown or loss.

Messaging uses these numbers sparingly and positively ("5 of the last 7 days. That's a solid week."). Streak counters and loss-framed language are not used. Rationale: lapses followed by self-blame are a known path to quitting; see the research note.

## Daily rhythm

Defaults, all adjustable or disabled by the owner:

| Slot | Time basis | Purpose | Shape |
| --- | --- | --- | --- |
| Morning | wake estimate + 10 to 20 min | Today's one or two priorities, sleep confirmation | One message, quick-reply buttons |
| Midday | habit windows | Prompts for habits due soon, anchored when possible | At most one message |
| Evening | wind-down window | Log-in: what got done, one win, tomorrow's first step | One message |
| Wind-down | bedtime − 60 to 90 min | Optional screens-down nudge | Opt-in |
| Weekly | Sunday evening by default | Review of consistency, one adjustment | Short, then a question |

Each slot is a scheduler job. When it fires, the coach checks quiet hours and budget before composing.

## Notification budget

- Daily cap on proactive messages (default 3, hard maximum configurable).
- Per-kind cooldowns (for example no habit prompt within 90 minutes of another).
- A cost estimate: low-value messages are dropped first when the cap is near.
- Back-off: if the last two proactive messages got no response, the next is delayed and phrased as a check on preferences ("Want me to message less this week?").
- Owner-requested reminders bypass the cap but still respect `silent` delivery during quiet hours unless marked urgent.

## Quiet hours and the sleep model

Quiet hours are derived from the sleep model's estimated sleep window plus a buffer, or from a manual window until enough data exists. See [../research/sleep/README.md](../research/sleep/README.md) for the algorithm. The sleep module exposes:

```go
type Model interface {
    Window(day civil.Date) (start, end time.Time, conf Confidence)
    InQuietHours(t time.Time) bool
    Record(ev SleepEvent)
    LastNight() (Estimate, bool)
}
```

## Lapse protocol

Triggered by a missed expected occurrence or an explicit "I skipped it":

1. **Acknowledge** without judgment and without exclamation.
2. **Normalize** in one sentence ("One miss doesn't change much. What matters is the next one.").
3. **Shrink or re-plan:** offer the tiny version for next time, or ask what got in the way and adjust the anchor.
4. **Ask one question**, not three.
5. **Never miss twice:** the next window after a miss gets a gentle, specific prompt.

If several misses accumulate, Kest asks whether the habit is still right instead of pushing harder, and offers to pause or resize it. Autonomy first.

## Adaptation and experiments

- **Timing adaptation:** for each prompt kind, track response latency by hour and weekday. A contextual bandit (Thompson sampling) chooses among a small set of candidate send times inside the allowed window. Bounded by quiet hours and budget.
- **Message variants:** several template variants per kind; opt-in micro-randomized comparisons by response and completion. Results are shown to the owner in the weekly review. See [../research/behavior-science/README.md](../research/behavior-science/README.md#decision-design-rules-for-kest).
- The owner can say "stop X" at any time and Kest turns that behavior off permanently.

## Messages

- Simple prompts use templates in `internal/coach/messages/` stored as data (embedded TOML), grouped by kind and tone.
- Richer messages (reviews, replanning, replies to feelings) are composed by the general model with a task prompt, the persona, and the structured facts. Output is validated against banned phrases and length limits before sending.

## Data flow example: morning check-in

1. Scheduler fires `coach.morning` at the estimated wake time plus 15 minutes, in the owner's timezone.
2. Coach asks the sleep module for `LastNight()` and whether the owner was already active.
3. Budget allows a message; quiet hours have ended.
4. Coach composes: a one-line greeting, a sleep line if confident, today's top priority from the `focus` module, a prompt for the first habit window.
5. Telegram sends it with buttons: "Slept about right", "Short night", "Plan my day".
6. The tap updates the sleep model and starts the next flow.

## What Phase 3 built

The design above is the target. Phase 3 implements the core of it and simplifies in four places, on purpose:

- **One engine, no module interface yet.** `internal/coach` is a single `Coach` with sub-packages `habit` (cadence, windows, consistency), `sleep` (events, inference, quiet hours), `budget` (caps, cooldowns, back-off) and `messages` (templates). The `Module` interface did not arrive in Phase 4 after all: the domains that needed the engine only needed hooks (ADR 0010).
- **One tick instead of one job per slot.** A scheduler job `coach.tick` runs every five minutes (`MisfireSkip`). Each tick works out what is due from the sleep model, the habits and today's history, and sends at most one message. This keeps the schedule correct when the sleep model changes, with no jobs to reschedule.
- **Templates only.** Every proactive message comes from `messages.toml` and passes `voice.Check`. Model-composed messages are a later step.
- **Sunday review replaces the evening check-in**, so there is one message that night, not two.

How a message gets out: `Tick` loads preferences, refreshes last night's estimate, builds the list of due candidates in priority order (the "message less?" question first, then morning, habit prompts, evening, wind-down, weekly) and asks the budget about each. The first one allowed is composed, sent through the `Outbox` and recorded in the notification history. The history is what the budget reads next time.

What the owner can do about it: ask in conversation (the agent calls `coach__set_preferences`), see the result with `/settings`, and silence everything with `/lockdown`. Button data is `area:action[:argument]` and stays under Telegram's 64 byte limit.

What the agent gets: the tools `create_habit`, `list_habits`, `log_habit`, `update_habit`, `log_sleep`, `sleep_report` and `set_preferences` (all under the normal risk policy), plus a short context block in the system prompt with the habits, last night's sleep and the current settings.

## Testing

- All time comes from `clock.Clock`; modules take a fake clock.
- Golden tests for message selection and the lapse protocol.
- Simulation tests: a synthetic owner with scripted responsiveness runs 60 days against the budget to confirm caps hold and quiet hours are never violated (`TestTwoMonthSimulation`). An owner who never replies must receive less than half as many messages as one who does. Adaptation is Phase 5.

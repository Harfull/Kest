# Sleep schedule mapping and estimation

## Question

Without a wearable, how can Kest map the user's sleep schedule, estimate sleep duration, and use that to time its messages?

## Sources

- Secondary: [Ciman and Wac 2019, smartphones as sleep duration sensors (iSenseSleep), JMIR mHealth](https://www.ncbi.nlm.nih.gov/pmc/articles/PMC6547769/)
- Secondary: [SensibleSleep, Bayesian model of sleep from smartphone events](https://arxiv.org/pdf/1608.06108)
- Secondary: [Touchscreen "tappigraphy" vs actigraphy, npj Digital Medicine 2019](https://link.springer.com/10.1038/s41746-019-0147-4)
- General knowledge (recalled, **not verified in this research**): adult sleep need of roughly 7 to 9 hours; the midpoint of sleep as a chronotype marker; sleep regularity as a health correlate.

## Findings

- **Phone activity carries signal.** One study with 14 subjects over up to 6 months estimated sleep duration from phone ON/OFF activity with an average error of about 7% (SD 4%) against smartwatch ground truth, and also estimated sleep start and wake times.
- **Event counts can work.** A Bayesian model on screen events found simple interaction counts can infer sleep patterns with reasonably high accuracy, with no special equipment.
- **Naive first and last touch is biased.** A study of 1,384 nights from 84 people found touchscreen-derived onset and wake times strongly correlated with actigraphy (R² about 0.9), but nearly everyone used the phone during the actigraphy-defined sleep period, and touch probability stayed above zero for roughly two hours after sleep onset and two hours before wake. Raw gaps therefore understate sleep or shift its edges.
- **Kest sees less than the phone does.** Kest observes only messages to the bot, which are far sparser than screen events. Gaps in chat will be longer than gaps in phone use, so inference from chat alone is coarse. It needs explicit signals.
- Ground truth in these studies is a wearable or actigraphy, not polysomnography. Sample sizes are small.

## Decision: a hybrid sleep model (`internal/coach/sleep`)

### Signals, strongest to weakest

1. **Explicit events:** "heading to bed", "good night", "I'm up", or tapping the inline buttons Kest offers at the usual times.
2. **Confirmations:** a once-a-day, low-effort question the morning after ("Around what time did you fall asleep? Roughly is fine.") with quick-reply buttons.
3. **Imports (later plugin):** wearable or phone-sleep data forwarded through a webhook or MCP server (for example, Health Connect exports).
4. **Inference from chat activity:** the longest inactivity gap in the user's usual night window.
5. **Declared schedule** from onboarding as the prior.

### Algorithm sketch

1. Convert all timestamps to the user's local time and work on a "sleep day" that runs noon to noon.
2. For each sleep day, choose the best available evidence by the order above.
3. For inference, find the longest gap of at least three hours inside the plausible night window. Estimate onset as gap start plus a learned onset offset and wake as gap end minus a learned wake offset. Offsets start at 20 minutes and update from confirmations using an exponential moving average, which also absorbs the "phone use leaks into sleep" bias.
4. Keep per-weekday robust statistics (median and median absolute deviation of onset and wake) over a rolling 28 days. Use circular statistics for clock times so 23:50 and 00:10 are close.
5. Emit an estimate with a **confidence** (`explicit`, `confirmed`, `inferred`, `prior`) and an uncertainty window. Never show false precision: "about 7 hours" not "7h 12m".
6. Compute sleep duration, midpoint, regularity (standard deviation of midpoint), and sleep debt against a user-set target (default 8 hours; adjustable 7 to 9).

### How it is used

- **Quiet hours:** estimated sleep window plus a 30 minute buffer; proactive messages are suppressed. Urgent user-requested reminders still fire but are silent (Telegram `disable_notification`).
- **Morning:** first message goes out 10 to 20 minutes after the usual wake time, never before a confirmed wake.
- **Wind-down:** one optional message roughly 60 to 90 minutes before the usual bedtime, opt-in, with a tiny action (screens down, set tomorrow's first task).
- **Workout and meal timing:** avoid placing intense-training prompts shortly before estimated bedtime.
- **Weekly report:** average duration, regularity and one practical suggestion. Language stays descriptive, not diagnostic.
- **Shift workers and travel:** timezone changes and irregular schedules are detected as large shifts; Kest asks instead of assuming.

### Limits and safety

- Estimates are approximate, personal and for scheduling only. Kest does not diagnose sleep disorders.
- If a user reports persistent insomnia, extreme sleep loss or distress, Kest suggests speaking with a professional and does not coach around it.
- Sleep logs are sensitive; they stay local and are exportable and deletable.

## Open questions

- How much accuracy does chat-only inference reach for this user? Track the error between inferred and confirmed times and show it in the weekly summary to decide whether to ask less.
- Read the primary papers for exact offsets and window choices before tuning defaults.

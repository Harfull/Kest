# Behavior science: staying consistent

## Question

What does the evidence say about helping one person build and keep habits (training, eating, sleep, focus), and how should that shape Kest's coaching engine, message timing and tone?

## Sources

- Secondary: [Hsu et al. 2024, scoping review of just-in-time adaptive interventions (JITAIs), Br J Health Psychol](https://www.ncbi.nlm.nih.gov/pmc/articles/PMC11583291/)
- Secondary: [Cotie et al. 2025, behavior change theories and techniques in physical-activity JITAIs](https://www.ncbi.nlm.nih.gov/pmc/articles/PMC12295828/)
- Secondary: [Hardeman et al. 2019, systematic review of physical-activity JITAIs](https://ueaeprints.uea.ac.uk/id/eprint/70272/)
- Secondary: [Lally et al. 2010 explained](https://www.thebehavioralscientist.com/articles/how-long-to-form-a-habit)
- Secondary: [Implementation intentions, NCI](https://cancercontrol.cancer.gov/brp/research/constructs/implementation-intentions)
- Secondary: [Fogg Behavior Model](https://www.thebehavioralscientist.com/?p=3058)
- Secondary: [Bidargaddi et al. 2018, micro-randomized trial of push notifications](https://pmc.ncbi.nlm.nih.gov/articles/PMC6293241)
- Secondary: [Bell et al. 2023, notification habituation trial (Drink Less)](https://pmc.ncbi.nlm.nih.gov/articles/PMC10337295)
- Secondary: [SDT and MI in a web-based tailored intervention RCT](https://www.ncbi.nlm.nih.gov/pmc/articles/PMC9058957/)
- Secondary: [Self-compassion and dietary lapses](https://www.ncbi.nlm.nih.gov/pmc/articles/PMC8451927/)
- Secondary: [Abstinence violation effect](https://pmc.ncbi.nlm.nih.gov/articles/PMC3296289)

Most claims below were obtained through search summaries, not by reading full papers. Effect sizes are quoted as reported and must be verified before being cited in user-facing copy.

## Findings

### 1. If-then plans (implementation intentions) are the best-supported single technique

- Form: "If situation X, then I will do Y."
- A 2006 meta-analysis of 94 studies is commonly cited with a medium-to-large effect (d around 0.65) on goal attainment (secondary source; verify).
- A 2025 meta-analysis of 642 independent tests reports effects from d = .27 to .66 across cognitive, affective and behavioral outcomes. Effects were larger when plans were in contingent if-then format, when the person was highly motivated, and when plans were rehearsed.
- Implication: plans work best for goals the user already wants. Kest should help write plans, not impose goals.

### 2. Habit formation is slow and variable; one miss is not fatal

- Lally et al. (2010): median 66 days to reach automaticity, with individuals ranging from 18 to 254 days. The headline comes from the subset of participants whose data fit the model, and the long end is extrapolated. The "21 days" figure is a myth.
- From the same study (recalled, **verify**): missing a single opportunity did not materially harm the habit curve.
- Implication: never frame a miss as failure. Plan for months, not weeks, and set expectations honestly.

### 3. Make it easy, anchor it, celebrate it (Fogg)

- B = MAP: behavior happens when Motivation, Ability and a Prompt converge. Design emphasis is on raising ability (making it tiny) rather than hoping for motivation.
- Recipe: "After I [anchor], I will [tiny behavior]. Then I celebrate."
- Evidence is mostly from Fogg's own coaching data. No controlled trial of Tiny Habits itself was found. The cue-based habit mechanism has independent support.
- Implication: use it as a design heuristic. Start goals tiny, attach them to existing routines, and give immediate positive feedback.

### 4. Just-in-time adaptive interventions are the right shape, with modest proof

- A JITAI delivers support triggered by the system, matched to a need in real time, and adapted to data collected since the intervention began.
- Reviews of dozens of JITAIs find the most common techniques are prompts and cues, behavioral goal setting and feedback on behavior. They also state that evidence is insufficient to say which theories or techniques work best, and that behavior-change effects are often small and temporary.
- Implication: Kest should be adaptive and honest. Log what it tries and whether the user responded, so tuning is data-driven. Do not promise miracles.

### 5. More messages are not better

- A micro-randomized trial (1,255 users, 89 days) found a tailored push notification raised the chance of app engagement in the next 24 hours by about 3.9%, with larger effects on weekends and at midday. Subgroup results were exploratory.
- A trial of the Drink Less app explicitly tested the hypothesis that users habituate to fixed daily notifications and disengage; outcome details were not retrieved.
- Implication: use a **notification budget**, vary content, learn each user's best windows, and let the user turn the volume down at any time.

### 6. Support autonomy

- Self-determination theory (autonomy, competence, relatedness) and motivational interviewing (MI) are theoretically aligned: MI creates the autonomy-supportive conditions SDT says sustain motivation.
- A web-based SDT and MI tailored program was tested in a 1,142-person RCT (outcomes not retrieved). A 2023 chatbot factorial experiment (102 participants, three weeks) found chatbots feasible for raising autonomous motivation, with competence and autonomy satisfaction as mediators.
- No large, long-term trials of LLM-based MI coaching were found.
- Implication: Kest asks and offers choices, reflects, affirms effort, and avoids commands and guilt. MI micro-skills (open questions, affirmations, reflections, summaries) guide prompts and the persona.

### 7. Handle lapses with self-compassion, not guilt

- The abstinence violation effect describes how a slip, interpreted as personal failure, can trigger giving up ("what the hell"). Evidence is mainly from addiction and dieting.
- In a two-week EMA study of dieters, self-compassion after a lapse was associated with stronger intention and self-efficacy to continue, and fewer negative reactions. Another trial found self-accountability reduced lapses faster while self-compassion produced more weight loss, so the evidence is mixed.
- Streak displays can raise short-term persistence, but a broken long streak may cause disengagement. Most streak evidence found was vendor material (**unverified**).
- Implication: show **consistency**, not an all-or-nothing streak. Use a rolling adherence rate and a "never miss twice" frame. After a miss, normalize, shrink the goal, and plan the next step.

## Decision: design rules for Kest

1. **Tiny first.** New habits start at a size the user rates 8 out of 10 or higher for "I could do this on my worst day".
2. **If-then plans** are created with the user for every habit and stored as structured data (`anchor`, `behavior`, `fallback`).
3. **Anchors over alarms.** Prefer prompts tied to a routine ("after your coffee") over clock times when the user has a stable routine.
4. **Consistency score, not streaks.** Display the last 7 and 28 day completion rate and "days showing up". Missing one day lowers a rate slightly; it never resets to zero. Add the rule "never miss twice" as a gentle reframe.
5. **Lapse protocol.** After a miss: acknowledge without judgment, give a one-line normalization, offer a smaller version of the habit or a re-plan, and ask a single question. No guilt, no comparison, no streak-loss language.
6. **Notification budget.** Default maximum three proactive messages per day, configurable. Each message type has a cooldown and a "value" estimate. Back off automatically after ignored messages and ask whether to change the schedule.
7. **Adaptive timing.** Track response latency by hour and weekday and shift check-ins toward windows with engagement (bandit approach). All adaptation is bounded by quiet hours.
8. **Autonomy language.** Offer options, ask permission for new nudges, let the user veto any behavior of Kest with one message ("stop morning check-ins").
9. **Celebrate immediately** and specifically (effort and process, not appearance or numbers alone).
10. **Honest expectations.** When asked how long a habit takes, say it varies, often weeks to months.
11. **Measurement without surveillance.** Keep logs lightweight and useful to the user; offer weekly summaries; allow export and deletion.
12. **No medical or eating-disorder-triggering behavior.** No calorie-shaming, no extreme targets, and a safe-completion path for signals of distress. See [persona safety](../../persona/voice.md#safety).

## Open questions

- Which of these rules improve outcomes for this user? Build an `experiments` table to compare message variants and times, using micro-randomized assignment with the user's consent.
- Should the consistency score weight recency? Start with a simple 28-day rate and revisit.
- Read the primary papers before quoting any statistic in a message.

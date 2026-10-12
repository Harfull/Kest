# nutrition

A light, non-judgmental log of meals and water, and up to three eating patterns you want to build. It has no calories, weights, targets or "good" and "bad" foods on purpose: the [voice guide](../../docs/persona/voice.md) rules out extreme dieting and body-focused talk, and a log that counts those invites them.

| Tool | Risk | What it does |
| --- | --- | --- |
| `log_meal` | write | Record a meal or snack in your words (breakfast, lunch, snack, dinner, other), with optional neutral descriptors: protein, vegetables, fruit, fiber, homemade, dairy, whole grains |
| `log_water` | write | Record glasses of water (1 to 20) |
| `nutrition_history` | read | What was logged over the last days (default 7), and on how many days |
| `set_eating_patterns` | write | Save up to three small patterns to build, such as "a vegetable with dinner". Patterns add something; they never restrict |

The weekly review gains one line, "Meals were logged on 5 day(s) this week." Nothing is evaluated.

The model is told, every turn, to log without comment, never give calorie or weight targets, never mention weight or call food good or bad, and, if you describe skipping meals to compensate, bingeing, purging or fear of eating, to drop the coaching tone, respond with care and suggest talking to a professional.

Records live in the plugin's key/value store, separate from conversation history, so `/forget` does not delete them. There are no settings.

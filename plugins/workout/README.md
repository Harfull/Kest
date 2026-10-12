# workout

A workout log and an optional weekly target. It records what you did and reports counts back, in conversation, in the coach's weekly review and to the model as context. It does not prescribe training, set targets on its own or give medical advice.

| Tool | Risk | What it does |
| --- | --- | --- |
| `log_workout` | write | Record a workout: kind, minutes, optional effort (1 to 10, how hard it felt) and note. Today by default, or any day up to 60 days back |
| `workout_history` | read | List the last days (default 14) with totals |
| `set_workout_target` | write | Set sessions per week (1 to 14), or 0 to clear |
| `delete_workout` | write | Remove an entry by its id, for one logged by mistake |

Say "I ran for half an hour" and Kest logs it. If you also keep a workout habit in the coach, the model is told to mark that habit done too, so the habit's consistency and the log agree. Reminders to train are habits, not part of this plugin.

The weekly review gains one line such as "Workouts this week: 2 session(s), 65 minutes, aiming for 3." The week starts on Monday. Counts are never graded: no streaks, no "behind", no loss framing, in line with [the voice guide](../../docs/persona/voice.md).

Records live in the plugin's key/value store, separate from conversation history, so `/forget` does not delete them. There are no settings. For pain or any health concern Kest suggests a professional and never encourages pushing through pain.

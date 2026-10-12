# focus

Pick the day's one to three priorities and work in timed stretches.

| Tool | Risk | What it does |
| --- | --- | --- |
| `set_priorities` | write | Set today's priorities (one to three), replacing the list |
| `list_priorities` | read | Show them and which are done |
| `complete_priority` | write | Mark one done by number |
| `start_focus` | write | Start a timer for a task, 5 to 120 minutes (default 25). You are told when time is up |
| `stop_focus` | write | End the timer early; the time spent is logged |
| `focus_summary` | read | Stretches and minutes today and this week, and what is running now |

A timer is something you asked for at a specific time, so its end message is sent like a reminder: "Your 25 minutes on the report are up. Take a minute to stretch, then decide the next small step." It is delivered even in quiet hours, which is also how reminders behave. The timer survives a restart: it is a scheduled job, and one missed by more than 30 minutes is dropped instead of announced late.

The model sees today's priorities and any running timer each turn, and is told to help you choose few priorities and offer a timer when you start work. The weekly review gains "Focus time this week: 4 stretch(es), 100 minutes." Priorities start fresh each day; nothing carries over as a debt.

Records live in the plugin's key/value store, separate from conversation history, so `/forget` does not delete them. There are no settings.

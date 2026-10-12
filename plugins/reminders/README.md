# reminders

Sets one-off and repeating reminders that Kest sends at the right time.

| Tool | Risk | What it does |
| --- | --- | --- |
| `create_reminder` | write | Schedule a reminder from a local date and time (`at`) or a repeat rule (`repeat`) |
| `list_reminders` | read | Show upcoming reminders with their ids |
| `cancel_reminder` | write | Cancel a reminder by id |

Repeat rules: `daily@07:30`, `weekdays@08:00`, `weekly:mon,thu@18:00`, `every:2h`.

## Behavior

- Reminders are scheduler jobs, so they survive restarts.
- Times are interpreted in the owner's time zone (`KEST_TIMEZONE`).
- A reminder the owner asked for is delivered even during quiet hours.
- A repeating reminder that is more than two hours late is skipped rather than sent stale.

## Settings

```toml
[plugins.reminders]
max_active = 50   # most reminders at once
```

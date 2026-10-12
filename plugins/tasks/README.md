# tasks

A simple to-do list the owner manages by chatting.

| Tool | Risk | What it does |
| --- | --- | --- |
| `add_task` | write | Add a task, optionally with a due date (`YYYY-MM-DD`) |
| `list_tasks` | read | List open tasks (or all, with `include_done`), soonest due first |
| `complete_task` | write | Mark a task done |
| `delete_task` | write | Delete a task |

Tasks are stored in the plugin's key/value store, separate from conversation history, so `/forget` does not delete them.

## Settings

```toml
[plugins.tasks]
max_open = 200   # most open tasks at once
```

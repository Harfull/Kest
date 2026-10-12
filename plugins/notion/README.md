# notion

Search, read, create and update pages in your Notion workspace through an internal integration. Setup: [Notion setup guide](../../docs/guides/notion-setup.md).

| Tool | Risk | What it does |
| --- | --- | --- |
| `search` | read | Find pages and databases by title, newest edits first |
| `get_page` | read | Read a page: properties and content (top-level blocks as text) |
| `query_database` | read | List rows in a database, with an optional Notion filter |
| `create_page` | write | Create a page under another page, or a row in a database, with properties and text content |
| `append_to_page` | write | Add text to the end of a page: for notes, logs and lists |
| `update_page_properties` | write | Change status, dates, checkboxes, select values or the title |
| `trash_page` | destructive | Move a page to the trash (restorable in Notion). Always asks |

Ids can be given as a Notion id or a pasted `notion.so` link. Text for `create_page` and `append_to_page` is plain text with `# ` headings, `- ` bullets, `1. ` numbers, `[ ] ` to-dos and `> ` quotes. Properties are given as simple values (text, numbers, true/false, lists of names, ISO dates) and checked against the database's properties first, so a misspelled property is reported by name instead of dropped.

## Settings and secret

```toml
[plugins.notion]
notion_version = "2026-03-11"   # the API version, pinned
max_chars = 6000                # longest page text shown to the model
```

The token is the secret `NOTION_TOKEN`: set `KEST_SECRET_NOTION_TOKEN` in the environment or `secrets.json`.

## Security and limits

- The integration sees only what you share with it, and its capabilities (read, update, insert) bound what Kest can do.
- Page text can be edited by other people. It is untrusted data, and once you read a page in a turn any later write needs your approval.
- Kest only talks to `api.notion.com`. Ids are validated before use, so a value cannot add path segments or query text.
- Requests are paced under Notion's average of three a second and a `Retry-After` of up to 20 seconds is honored; a longer wait is reported instead of blocking the chat.
- Not built yet: mirroring the daily coach log into a Notion database automatically. You can ask Kest to append to a page whenever you want a log there.

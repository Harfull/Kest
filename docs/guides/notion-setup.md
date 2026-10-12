# Notion setup

The [notion plugin](../../plugins/notion/README.md) uses an internal integration token. The integration sees only the pages and databases you share with it.

## 1. Create the integration

1. Open [notion.so/profile/integrations](https://www.notion.so/profile/integrations) and create a new **internal** integration for your workspace.
2. Under **Capabilities** choose what Kest may do: **Read content** is needed to search and read; add **Update content** and **Insert content** to let it edit and create pages. Without them the matching tools report a permission error, which is a handy way to keep Kest read-only.
3. Copy the **Internal integration secret** (it starts with `ntn_` or `secret_`).

## 2. Share pages with it

Notion integrations start with access to nothing. Open each page or database you want Kest to use, choose the **...** menu, then **Connections**, and add your integration. Access covers the page and everything inside it. A search that returns nothing usually means nothing has been shared yet.

## 3. Configure Kest

Add `notion` to `KEST_PLUGINS` and set the token as `KEST_SECRET_NOTION_TOKEN` (the **Notion Token** startup variable on Pterodactyl, or `secrets.json`). Restart, check `/status`, and ask "search Notion for my weekly plan".

You can paste a Notion link instead of an id, and ask for things like:

- "add a line to my Journal page: ran 30 minutes"
- "create a page under Projects called Kitchen plan with these three steps"
- "in my Workouts database, add Push day, 50 minutes, kind strength, done"
- "mark the Dentist task done"

Databases are read and written through their properties: Kest checks property names against the database and reports a misspelled one by name. Property types it can set are title, text, number, checkbox, select, status, multi-select, date, URL, email and phone. Relations, people and files are shown but cannot be set from here.

## API version

The plugin pins the Notion API version `2026-03-11` (setting `notion_version`), the version that uses data sources and `in_trash`. A different version may need code changes, so change it only after checking [Notion's upgrade guides](https://developers.notion.com/page/changelog). The research behind the choices is in [../research/integrations/notion.md](../research/integrations/notion.md).

## Troubleshooting

| Message | What to do |
| --- | --- |
| `the integration token was rejected` | The token is wrong or was regenerated; update `KEST_SECRET_NOTION_TOKEN` |
| `that was not found, or the integration cannot see it` | Share the page or database with the integration (section 2) |
| `does not have permission` | Enable the matching capability on the integration |
| `the database has no property called ...` | The message lists the real property names |
| `requests to Notion are being rate limited` | Wait a minute; Kest already paces requests under Notion's limit |

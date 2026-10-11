# Notion

## Question

How should Kest read and write Notion, given API versions and rate limits?

## Sources

- Primary: [Notion request limits](https://developers.notion.com/reference/request-limits)
- Primary: [Notion rate limits](https://developers.notion.com/reference/rate-limits)
- Primary: [Notion API changelog](https://developers.notion.com/page/changelog)

## Findings

- Rate limit: an average of **3 requests per second per connection**, with bursts allowed. Since the 16 June 2026 changelog entry there is also a **per-workspace** limit shared by all connections and scaled to plan. Over-limit requests return 429 (and 529 for overload) with a `Retry-After` header in integer seconds; `additional_data.rate_limit_reason` says which limit was hit.
- Versioning: API version `2025-09-03` introduced **data sources** (what used to be a "database" is now a data source inside a database container). Version `2026-03-11` followed, with breaking changes such as `position` replacing `after` on append-block-children. Always send an explicit `Notion-Version` header.
- Auth: an internal integration token is the simplest for a single user. The owner shares specific pages or databases with the integration.
- No official Go SDK was found in the research; the official SDK is JavaScript. A small in-house client over `net/http` is realistic.

## Decision

- Write `plugins/notion` with a small client: token auth, pinned `Notion-Version`, a shared token-bucket limiter at 2 to 3 req/s, and `Retry-After` handling.
- Tools (v1): `notion.search`, `notion.get_page`, `notion.query_data_source`, `notion.create_page`, `notion.append_to_page`, `notion.update_page_properties`. Writes are `write` risk; archiving or deletion is `destructive`.
- The coach engine can optionally mirror daily logs (workout, meals, sleep) into a user-chosen Notion data source. Notion is a view, SQLite stays the source of truth.

## Open questions

- Whether to adopt the `2026-03-11` version immediately or pin `2025-09-03` for stability. Decide when implementing; record in an ADR.

# webfetch

Fetches a web page and returns its readable text so Kest can summarize a link.

| Tool | Risk | What it does |
| --- | --- | --- |
| `fetch_url` | read | Fetch an http(s) URL and return the page title and text |

## Safety

- The result is marked **untrusted**. The agent wraps it as data, and any state change made afterwards in the same turn needs the owner's approval.
- Private, loopback, link-local and cloud-metadata addresses are refused at connection time, so a link cannot be used to reach internal services. Every redirect is re-checked.
- Only `text/*`, HTML and JSON are read; bodies are capped at 2 MB and the text at `max_chars`.

## Settings

```toml
[plugins.webfetch]
max_chars  = 6000
user_agent = "Kest/0.1 (+https://github.com/Harfull/Kest)"
```

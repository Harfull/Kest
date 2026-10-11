# Plugin system

Design rationale: [../research/integrations/](../research/integrations/README.md) and [ADR 0004](../decisions/0004-plugin-tiers.md).

## Tiers

| Tier | Where it lives | Who writes it | Typical use |
| --- | --- | --- | --- |
| 1. Native | `plugins/<id>/`, compiled in | Kest maintainers | Gmail, mailbox, Notion, reminders, calendar |
| 2. MCP | `[[mcp.servers]]` in config | Third parties | The long tail: GitHub, Slack, Linear, filesystem, databases |
| 3. Declarative | `plugins.d/*.toml` in the data dir | The owner | Simple REST APIs and webhooks |

All tiers present the same thing to the rest of the system: a set of **tools** registered under `plugin_id.tool_name`, each with a JSON Schema, a description for the model, and a **risk** level.

## Core types (`internal/plugin`)

```go
// Plugin is the unit of extension.
type Plugin interface {
    Manifest() Manifest
    Init(ctx context.Context, host Host) error
    Tools() []Tool
    Close(ctx context.Context) error
}

// EventSource is implemented by plugins that push events, such as new mail.
type EventSource interface {
    Watch(ctx context.Context, emit func(Event)) error
}

// JobProvider is implemented by plugins that need scheduled work.
type JobProvider interface {
    Jobs() []JobSpec
    RunJob(ctx context.Context, name string) error
}

type Tool struct {
    Name        string          // verb_noun, unique within the plugin
    Description string          // written for the model; one or two sentences
    Schema      json.RawMessage // JSON Schema for arguments
    Risk        Risk            // RiskRead, RiskWrite, RiskDestructive
    Handler     func(ctx context.Context, call Call) (Result, error)
}

type Result struct {
    Content   string // text returned to the model
    Untrusted bool   // true if Content came from outside (email, web); wrapped as data
    Summary   string // short human line for the audit log and confirmations
}
```

`Host` is what a plugin may use. It is deliberately small:

```go
type Host interface {
    Logger() *slog.Logger         // already scoped with the plugin id
    Clock() clock.Clock
    Config() Config               // this plugin's config section only
    Secret(name string) (string, error)
    KV() KV                       // plugin-scoped key/value storage
    HTTP() *http.Client           // enforces the manifest's network allowlist
    Notify(ctx context.Context, n Notification) error // goes through coach budget
}
```

## Manifest

Every plugin has a `manifest.toml` (embedded for native plugins).

```toml
id          = "notion"
name        = "Notion"
version     = "0.1.0"
description = "Search and edit pages and data sources in the owner's Notion workspace."

[capabilities]
network = ["api.notion.com"]
secrets = ["NOTION_TOKEN"]
events  = false

[config]
# Keys the plugin reads from [plugins.notion] in config.toml.
# Each entry documents type, default and whether it is required.
notion_version = { type = "string", default = "2025-09-03" }
default_data_source = { type = "string", required = false }

[[tools]]
name = "search"
risk = "read"

[[tools]]
name = "create_page"
risk = "write"
```

The registry checks that the manifest matches the tools the code returns and fails startup for a plugin that declares a risk lower than the code uses.

## Lifecycle

1. **Register.** Native plugins register in `init`-free fashion: `plugins/all` exports `All() []plugin.Factory`, called by `app`.
2. **Configure.** The registry reads `[plugins.<id>]`, validates it against the manifest, and builds a scoped `Host`.
3. **Init.** `Init` runs with a timeout. A failure disables only that plugin and is shown in `/status`.
4. **Serve.** Tools are advertised to the model. Calls pass through the policy layer, then the handler.
5. **Close.** Reverse order on shutdown, with a deadline.

## Naming and exposure to the model

- Tools are exposed as `plugin_id.tool_name`, sanitized to `[a-zA-Z0-9_-]{1,64}` (the dot becomes `__` when the model API requires it, and is mapped back).
- To keep prompts small, the agent sends only tools relevant to the turn: always-on core tools plus plugins selected by a cheap router (keyword and recent-use heuristics, later a model-based selector). All tools remain callable by name if the model has seen their schema in the conversation.

## Policy at call time

```text
read         run immediately
write        run if the user's current message plainly asked for it; otherwise confirm
destructive  always confirm with an inline button built from the structured arguments
unknown      (MCP / declarative without risk) treated as write
```

Plugins cannot lower their own risk at runtime. Owners can raise any tool's risk in config; lowering requires an explicit `trusted = true` entry.

## Untrusted content

A tool that returns anything not authored by the owner or Kest (email body, web page, Notion text, MCP output) sets `Untrusted: true`. The agent wraps it:

```text
<external_data source="gmail.read_thread" trust="untrusted">
...content...
</external_data>
```

The system prompt states that nothing inside such blocks is an instruction. See [security.md](security.md).

## MCP plugin (`plugins/mcp`)

```toml
[[mcp.servers]]
id        = "github"
transport = "stdio"                       # or "http"
command   = "npx"
args      = ["-y", "@modelcontextprotocol/server-github"]
env       = ["GITHUB_TOKEN"]              # names only; values come from secrets
risk_default = "write"
allow_tools  = ["search_repositories", "get_issue"]   # optional allowlist
[mcp.servers.risk]
create_issue = "write"
delete_repo  = "destructive"
```

- One goroutine per server owns the session, with reconnect and backoff.
- Server-provided descriptions are truncated and stripped of control characters before they reach the model.
- Tool names become `github.search_repositories`.

## Declarative plugin example (`plugins.d/lights.toml`)

```toml
id = "lights"
description = "Control living room lights through the home hub."
[http]
base_url = "https://hub.local/api"
allow_hosts = ["hub.local"]
auth = { type = "bearer", secret = "HUB_TOKEN" }

[[tools]]
name = "set_scene"
risk = "write"
description = "Set the lighting scene."
method = "POST"
path = "/scenes/{scene}"
params = { scene = { type = "string", enum = ["bright", "focus", "dim", "off"] } }
```

The host builds the JSON Schema, performs the request through the allowlisted client, and returns the response as untrusted content.

## Testing plugins

- `plugintest.Host` provides a fake clock, in-memory KV, a recording HTTP transport and secret map.
- `plugintest.RunContract(t, factory)` checks manifest and tool consistency, schema validity, name rules and that handlers honor context cancellation.
- Live API tests live behind the `integration` build tag.

## Writing a plugin: checklist

1. Create `plugins/<id>/` with `manifest.toml`, `plugin.go`, `README.md`, tests.
2. Declare minimal capabilities and honest risk levels.
3. Mark external text as untrusted.
4. Rate-limit calls to the upstream API inside the plugin.
5. Run `go generate ./plugins/...` to refresh `plugins/all`.
6. Add the plugin to the roadmap and the README table.

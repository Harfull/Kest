# Plugin system

Design rationale: [../research/integrations/](../research/integrations/README.md) and [ADR 0004](../decisions/0004-plugin-tiers.md). Safety model: [security.md](security.md).

## Tiers

| Tier | Where it lives | Who writes it | Status |
| --- | --- | --- | --- |
| 1. Native | `plugins/<id>/`, compiled in | Kest maintainers | Built: `reminders`, `tasks`, `webfetch`, optional `browser` |
| 2. MCP | `[[mcp.servers]]` in config | Third parties | Planned, Phase 5 |
| 3. Declarative | `plugins.d/*.toml` in the data dir | The owner | Planned, Phase 5 |

All tiers present the same thing to the rest of the system: **tools** registered under `plugin_id.tool_name`, each with a JSON Schema, a description for the model, and a **risk** level.

## Core types (`internal/plugin`)

```go
type Plugin interface {
    Manifest() Manifest
    Init(ctx context.Context, host Host) error
    Tools() []Tool
    Close(ctx context.Context) error
}

// Implemented by plugins that want to teach the model how to use their tools.
// The text is constant, trusted guidance added to the system prompt.
type Prompter interface {
    PromptHint() string
}

// Implemented by plugins that schedule work (manifest: capabilities.jobs = true).
type JobProvider interface {
    JobNames() []string
    RunJob(ctx context.Context, name, payload string) error
}

type Tool struct {
    Name        string          // verb_noun, lower snake case, unique within the plugin
    Description string          // written for the model
    Schema      json.RawMessage // JSON Schema object for the arguments
    Risk        Risk            // RiskRead, RiskWrite, RiskDestructive; must match the manifest
    Outbound    bool            // sends data to third parties: always confirmed
    Describe    func(args json.RawMessage) string // renders arguments for approval prompts (optional)
    AskReason   string          // completes "I am asking because ..." in the prompt (optional)
    Grantable   bool            // the owner may approve this plugin's grantable tools for one turn
    GrantCovers func(ctx context.Context, args json.RawMessage) bool // narrows a grant per call (optional)
    Handler     func(ctx context.Context, call Call) (Result, error)
}

type Result struct {
    Content   string // text returned to the model
    Untrusted bool   // came from outside the owner and Kest: wrapped as data, taints the turn
    Summary   string // short line for the audit log
    Image     *Image // pixels for this turn's model input (untrusted, never stored)
}
```

`Host` is everything a plugin may use. Every handle is scoped to that plugin:

```go
type Host interface {
    Logger() *slog.Logger                  // scoped with the plugin id
    Clock() clock.Clock                    // never call time.Now
    Location() *time.Location              // the owner's time zone
    Config() Config                        // this plugin's validated settings
    Secret(name string) (string, error)    // only names the manifest declared
    KV() KV                                // plugin-scoped key/value storage
    Jobs() Jobs                            // schedule persistent jobs (own jobs only)
    HTTP() *http.Client                    // allowlisted hosts, no private addresses
    Notify(ctx context.Context, n Notification) error
}
```

Plugins that keep the owner's own data can also implement `ContextProvider` (`PromptContext(ctx) string`, added to the system prompt each turn) and `Digester` (`WeeklyDigest(ctx) string`, a line in the weekly review). A non-urgent `Host.Notify` goes through the coach's notification budget (quiet hours, daily cap, minimum gap) and returns `plugin.ErrNotSent` when held back, so a plugin that still cares can try again later. Urgent notifications (a reminder or timer the owner asked for) skip the budget. The email plugins use this for opt-in new-mail nudges, polled by a scheduled job; there is no push `EventSource`.

Shared plugin code lives in `plugins/internal/` (`mailkit`, `logbook`), which only plugins can import.

## Manifest

Every plugin embeds a `manifest.toml`. The registry refuses a plugin whose code disagrees with it.

```toml
id          = "webfetch"
name        = "Web fetch"
version     = "0.1.0"
description = "Fetch a web page and return its readable text."

[capabilities]
network = ["*"]            # hosts the plugin may contact; "*" = any public host
secrets = []               # names readable through Host.Secret
jobs    = false            # true if the plugin implements JobProvider

[config]                   # settings under [plugins.<id>] in config.toml
max_chars = { type = "int", default = 6000 }      # types: string, int, float, bool

[[tools]]
name = "fetch_url"
risk = "read"
```

Load-time checks (`internal/plugin/registry.go`):

- the manifest is valid and its `id` equals the id it was registered under;
- every code tool is declared with the **same** risk, and every declared tool exists;
- `capabilities.jobs` matches whether the plugin implements `JobProvider`;
- settings from `config.toml` are known, correctly typed, and required ones are present;
- `Init` finishes within 30 seconds.

A plugin that fails any check is disabled and reported by `/status`; the others keep working. An unknown id in `enabled_plugins` is reported the same way.

## Configuration

```toml
enabled_plugins = ["reminders", "tasks", "webfetch"]   # or KEST_PLUGINS=reminders,tasks

[plugins.reminders]
max_active = 50
```

Settings for a plugin that is not enabled are a configuration error. Secrets are read from `KEST_SECRET_<NAME>` environment variables, for example `KEST_SECRET_NOTION_TOKEN`, and only for names the manifest declared.

## Naming

Tools are exposed to the model as `plugin__tool` (function names cannot contain dots) and shown to people as `plugin.tool`. The built-in memory tools (`memory__remember`, `memory__recall`, `memory__forget_fact`) follow the same rules and cannot be shadowed by a plugin.

## Tool routing

Every tool definition costs tokens on every model call, and models choose worse from long lists. Up to 24 tools, all are shown. Beyond that, `internal/agent/router.go` shows the `memory`, `coach` and `browser` tools (the browser is needed for follow-ups such as "scroll down", whatever the message says) plus the 14 best matches for the owner's message. Matching is plain word overlap (lower-cased, crudely stemmed) between the message and each tool's plugin id, name and description; name matches count triple. There is no model call, so it adds no cost or latency, and the same message always selects the same tools.

The trade-off is that a tool the message never hints at is hidden for that turn. Write tool descriptions with the words an owner would use ("email", "note", "calendar"), not only internal terms. A hidden tool that the model calls anyway still runs, under the usual policy.

## Policy at call time

Implemented in `internal/agent/policy.go`.

```text
read         run immediately
write        run immediately, unless untrusted content was read earlier in this turn: then ask
destructive  always ask
outbound     (Tool.Outbound) treated as destructive whatever Risk says
```

"Ask" means an approval message in Telegram showing the exact arguments with **Go ahead** and **Skip** buttons. The turn waits (two minutes by default). No answer, a late press, a forged id or a restart all mean the action does not run.

### Task grants

A multi-step job such as browsing would otherwise need a tap per step. A tool marked `Grantable` makes its prompt offer a third button, **Allow for this task**. Pressing it approves that call and, for the rest of the same turn only, the plugin's other grantable tools, except where `GrantCovers` says a particular call is outside what the owner saw. The plugin decides what a grant cannot reach (the browser stops at purchases, credentials, other websites and anything addressed by a raw selector). A grant never applies to a tool that is not `Grantable`, never outlives the turn, and a `RiskDestructive` tool cannot be `Grantable`. Taint still applies: a grant only removes the prompts, it does not make page content trusted.

Plugins cannot lower their own risk at runtime, and the risk in the manifest is checked against the code at load.

## Untrusted content

A tool that returns anything the owner and Kest did not write (a web page, an email body, a third-party API response) sets `Result.Untrusted`. The agent then:

1. wraps the content as `<external_data source="plugin.tool" trust="untrusted">…</external_data>` after stripping control characters, truncating, and neutralizing anything that looks like the wrapper's own tags;
2. marks the rest of the turn **tainted**, so state-changing tools need approval;
3. stores the turn's reply with origin `kest:tainted`, so summaries leave it out;
4. quarantines any memory saved during the turn.

## Jobs and notifications

A plugin with `capabilities.jobs = true` schedules work through `Host.Jobs()` and handles it in `RunJob`. Jobs are persistent scheduler jobs owned by `plugin:<id>` and named `<id>/<job>`; a plugin can only list or cancel its own. `Host.Notify` sends the owner a message; `Urgent` is for things the owner asked to be told at a specific time, such as a reminder.

## Network

`Host.HTTP()` returns a client that:

- only contacts hosts in `capabilities.network` (or any public host for `"*"`) over http or https;
- refuses private, loopback, link-local, carrier-grade NAT, multicast and cloud-metadata addresses **at connection time**, so a name that resolves to an internal address is refused too;
- re-checks every redirect and stops after five;
- has no proxy and a 20 second timeout.

## Writing a plugin

`Host.DialContext` applies the manifest host allowlist and public socket-address checks to TCP connections (including the browser's HTTPS proxy tunnels). Plugins declaring `capabilities.files = true` receive `Host.DataDir()`, a private directory under `<data dir>/plugins/<id>`. Native plugins remain trusted in-process code; this API is a scoped contract, not hostile-code isolation.

1. Create `plugins/<id>/` (the id is lower-case letters and digits; it becomes the Go package name) with `manifest.toml`, `plugin.go` defining `func New() plugin.Plugin`, a `README.md` and tests.
2. Declare minimal capabilities and honest risk levels. Mark external text `Untrusted`.
3. Keep tool descriptions short and written for the model. Validate every argument; treat arguments as untrusted input too.
4. Run `make gen` to update `plugins/all`, which lists the built-in plugins. CI fails if it is stale.
5. Test with `internal/plugin/plugintest`: `NewHost()` gives a fake clock, in-memory storage and jobs, and `RunContract(t, New, cfg)` checks the manifest/code agreement and tool shape.
6. Add the plugin to the README table and the roadmap.

## MCP and declarative plugins (planned)

The designs from the research stand:

- **MCP:** `[[mcp.servers]]` entries (stdio or HTTP) whose tools are bridged into the registry as `server.tool`, with unknown tools defaulting to `write` risk, descriptions truncated and sanitized, and results always untrusted.
- **Declarative:** a TOML file describing HTTP calls and parameter schemas, executed through the allowlisted client, with results untrusted.

Both use the same policy, naming and approval flow as native plugins.

# Model Context Protocol (MCP)

## Question

Can MCP give Kest broad tool coverage safely, and what does the Go SDK offer?

## Sources

- Primary: [modelcontextprotocol/go-sdk](https://github.com/modelcontextprotocol/go-sdk) (official, maintained with Google)
- Secondary: [pkg.go.dev listing for the SDK](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk@v1.7.0-pre.3)
- Primary: [Quick start](https://go.sdk.modelcontextprotocol.io/quick_start/)

## Findings

- The official Go SDK supports both servers and clients. License: Apache-2.0 for new contributions, MIT for existing code.
- Client API: `mcp.NewClient(&mcp.Implementation{...}, opts)`, then `client.Connect(ctx, transport, nil)` returns a session. Sessions expose tool listing and `CallTool`.
- Transports include `CommandTransport` (spawn a subprocess and talk over stdio), plus SSE and streamable HTTP clients.
- Spec versions: releases from v1.7.0 target the 2026-07-28 spec and still support 2025-11-25, 2025-06-18, 2025-03-26 and 2024-11-05. The newest tag seen was a pre-release; pin a stable tag at implementation time.
- MCP tools expose JSON Schema parameters and descriptions, which map directly onto OpenRouter tool definitions.
- MCP servers are third-party code. Their descriptions and outputs are untrusted input and a known prompt-injection channel. See [security](../security/README.md).
- Many stdio MCP servers are Node or Python programs. A Pterodactyl container must contain the runtime; remote (HTTP) MCP servers avoid that.

## Decision

- Implement an **MCP client plugin** (`plugins/mcp`) that reads `[[mcp.servers]]` from config, connects at startup, lists tools and registers them into the same registry as native tools under the name `server_id.tool_name`.
- Each server entry has an explicit `allow_tools` list or `risk_default`; unknown tools default to **write** risk and require confirmation.
- Tool descriptions from servers are length-limited and sanitized before they reach the model.
- Support stdio first, then HTTP.
- Restrict the environment passed to subprocesses to what the config names.

## Open questions

- Whether to expose Kest itself as an MCP server (so other agents can use its memory and schedule). Not planned for v1.
- Runtime availability of `npx` or `uvx` on the target host.

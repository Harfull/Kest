# Go plugin system options

## Question

Which mechanism should extend Kest with new integrations?

## Sources

- Secondary: [wazero documentation](https://wazero.io/docs/) and [project page](https://wazero.io/)
- Secondary: [HashiCorp go-plugin](https://docsearch.algolia.com/mcp/docs/repo/hashicorp/go-plugin) summaries
- Secondary: [Plugins and Go (FOSDEM 2016)](https://archive.fosdem.org/2016/schedule/event/plugins/)
- General knowledge for compile-time registration (Caddy-style); not separately sourced.

## Findings

| Option | Pros | Cons |
| --- | --- | --- |
| Native `plugin` package | Same process, fast | Needs identical Go toolchain and build environment, Linux only, no isolation. Rarely used in practice. |
| HashiCorp `go-plugin` | Process isolation, battle-tested (Terraform, Vault), any language through gRPC | One process per plugin, RPC overhead, operational weight for a single small container |
| WebAssembly (wazero) | Pure Go, no CGO, sandboxed by default, panics contained | Numeric-only ABI, richer data needs serialization, ecosystem less mature |
| Compile-time registration | Fastest, type-safe, debuggable, single binary | Adding a plugin requires a rebuild; no isolation |
| MCP servers | Huge existing ecosystem, process isolation, language agnostic | Tool quality varies; every result must be treated as untrusted |

## Decision

- Native integrations use **compile-time registration** from `plugins/all`. This fits "single static binary" and Pterodactyl hosting.
- Long-tail integrations use **MCP**. This gives the "hundreds of tools" without writing them.
- Declarative HTTP plugins fill the gap for simple APIs.
- WASM and `go-plugin` are revisited only if third parties need to ship closed-source or untrusted plugins.

## Open questions

- Hot reload: compile-time plugins cannot reload. MCP and declarative plugins can be reloaded on SIGHUP or a `/reload` admin command.

# 0004. Three plugin tiers: native, MCP, declarative

- Status: Accepted
- Date: 2026-10-11

## Context

The goal is "hundreds" of integrations, including Gmail, a custom mailbox and Notion, while staying a single binary and staying safe. See [research](../research/integrations/README.md).

## Decision

1. **Native plugins** compiled into the binary for core integrations.
2. **MCP client plugin** to bridge any MCP server's tools into the registry.
3. **Declarative HTTP plugins** defined in TOML for simple APIs.

All tiers use the same registry, risk levels, capability checks and confirmation flow. WASM and `go-plugin` are deferred.

## Consequences

- Broad coverage without writing every integration.
- Uniform safety model; MCP and declarative tools default to `write` risk.
- Native plugins require a rebuild; MCP and declarative can reload at runtime.

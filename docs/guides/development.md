# Development

## Prerequisites

- Go (version in `go.mod`)
- `make` (on Windows use Git Bash, WSL or run the underlying `go` commands)
- [golangci-lint](https://golangci-lint.run/welcome/install/)
- A C compiler if you want `-race` locally (`make test` uses it; CI always does)

## Loop

```bash
cp .env.example .env     # fill in the three required values
set -a; source .env; set +a
go run ./cmd/kest
```

## Checks

```bash
make check     # fmt-check, egg-check, gen-check, vet, lint, test
make cover     # writes coverage.html
make egg       # regenerate the Pterodactyl egg after editing install.sh or variables
make gen       # regenerate plugins/all after adding or removing a plugin
```

Without `make`:

```bash
gofmt -l .
go run ./scripts/sync-egg -check
go run ./scripts/genplugins -check
go vet ./...
golangci-lint run
go test ./...
```

## Conventions

Read [AGENTS.md](../../AGENTS.md) first. Highlights:

- Conventional commits, committed in logical units and pushed in batches.
- `internal/clock` instead of `time.Now` in logic; `log/slog` only.
- Docs change in the same batch as the code they describe.

## Adding a setting

1. Add the field to `internal/config/config.go` with a TOML tag and a default.
2. Add its environment variable in `internal/config/env.go` and validation in `validate.go`.
3. Add it to `config.example.toml`, `.env.example` and [configuration.md](configuration.md). Tests fail if the examples drift.
4. If it should be editable in a panel, add an egg variable in `scripts/sync-egg/main.go` and run `make egg`.

## Adding a plugin

See "Writing a plugin" in [../architecture/plugins.md](../architecture/plugins.md).

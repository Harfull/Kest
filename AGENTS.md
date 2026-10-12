# AGENTS.md

Repository conventions for **Kest**, a personal accountability assistant that runs as a Telegram bot, written in Go.

This file is the source of truth for how code, docs, commits and releases are done here. It is written for humans and AI agents alike. If a convention is missing, add it here in the same change that needs it.

## 1. Project in one paragraph

Kest helps one user stay consistent: workouts, diet, sleep, focus, and not procrastinating. It talks through Telegram, reasons through OpenRouter models (general, transcription, TTS), schedules its own check-ins, and connects to outside services (Gmail, SMTP, Notion, and many more) through a plugin system. It ships as a single static binary and runs on a Pterodactyl panel.

Read these before making non-trivial changes:

| Doc | Purpose |
| --- | --- |
| [docs/architecture/overview.md](docs/architecture/overview.md) | System design, package layout, data flow |
| [docs/roadmap.md](docs/roadmap.md) | Phased delivery plan and status |
| [docs/research/](docs/research/) | Findings behind every major decision |
| [docs/persona/voice.md](docs/persona/voice.md) | How Kest talks. Binding for all user-facing text |
| [docs/decisions/](docs/decisions/) | Architecture Decision Records (ADRs) |

## 2. Principles

1. **Consistency over intensity.** The product goal shapes the engineering: small, reliable, predictable beats clever.
2. **Modular by default.** Every integration and every behavior module is a plugin behind an interface. Core never imports a plugin.
3. **Boring technology.** Prefer the standard library. Add a dependency only when it removes real complexity, and record why in an ADR if it is large.
4. **Single binary, single data dir.** No external services required to run. SQLite for state, files for config.
5. **Fail loudly in logs, gently to the user.** Errors are wrapped and logged with context; the user sees a short, calm message.
6. **Private by default.** Personal data stays local. Secrets never reach logs, commits or LLM prompts.

## 3. Repository layout

The authoritative tree lives in [docs/architecture/file-structure.md](docs/architecture/file-structure.md). Summary:

```text
cmd/kest/          entrypoint only, no logic
internal/          all application code (not importable by others)
  app/             wiring, lifecycle, dependency injection
  config/          config loading and validation
  telegram/        bot transport
  llm/             OpenRouter client and model routing
  agent/           conversation loop, tool calling, memory
  scheduler/       jobs, reminders, quiet hours
  coach/           motivation engine, habits, sleep model
  plugin/          plugin registry, manifest, capability model
  store/           SQLite access and migrations
plugins/           built-in integrations, one directory each
deploy/pterodactyl/ egg and install scripts
docs/              architecture, research, ADRs, guides
.github/           workflows, issue and PR templates
```

Rules:

- `cmd/` contains `main.go` files that only parse flags, build the app and run it.
- Code under `internal/` must not import from `plugins/`. Plugins import from `internal/plugin` (the public SDK surface re-exported in `pkg/kestsdk` once stable).
- One package, one responsibility. If a package name needs "and", split it.
- No `util`, `common`, `helpers` or `misc` packages.

## 4. Code conventions (Go)

### 4.1 Toolchain

- Go version is pinned in `go.mod`. Do not use features newer than that version.
- Format with `gofmt` (via `goimports`). CI fails on unformatted code.
- Lint with `golangci-lint` using the repo's `.golangci.yml`. CI fails on findings.
- Run `make check` before every commit. It runs format check, vet, lint and tests.

### 4.2 Style

- Follow [Effective Go](https://go.dev/doc/effective_go) and the [Google Go Style Guide](https://google.github.io/styleguide/go/) where they agree; the more specific rule below wins.
- Prefer small functions and early returns. Keep nesting under three levels.
- Accept interfaces, return concrete types. Define interfaces in the package that **uses** them, and keep them small.
- Pass `context.Context` as the first parameter of anything that does I/O, blocks or can be cancelled. Never store a context in a struct.
- No package-level mutable state and no `init()` side effects. Wire dependencies explicitly in `internal/app`.
- No `panic` outside of `main` startup and truly impossible states. Never panic on user or network input.
- Concurrency: every goroutine has an owner, a stop condition and a recovered panic boundary. Use `errgroup` or an explicit `sync.WaitGroup`. Document who closes each channel.
- Time: inject a `Clock` interface. Never call `time.Now()` in business logic. Store UTC, convert to the user's timezone only at the edges.
- Logging: structured `log/slog` only. Never `fmt.Println` or the `log` package outside `main`.
- Generics only when they remove duplication across three or more types.

### 4.3 Errors

- Wrap with context: `fmt.Errorf("fetch gmail threads: %w", err)`. The message names the operation, not the word "failed".
- Lowercase, no trailing punctuation.
- Use sentinel errors (`var ErrNotFound = errors.New(...)`) or typed errors for conditions callers branch on. Check with `errors.Is` / `errors.As`.
- Log an error **once**, at the layer that handles it. Do not log and return.
- User-facing error text comes from the persona layer, never from `err.Error()`.

### 4.4 Naming

| Thing | Rule | Example |
| --- | --- | --- |
| Package | short, lowercase, singular, no underscores | `scheduler`, `store` |
| File | lowercase `snake_case.go`; tests `_test.go` | `quiet_hours.go` |
| Exported identifier | `PascalCase`, no package stutter | `scheduler.Job`, not `scheduler.SchedulerJob` |
| Unexported identifier | `camelCase` | `nextRun` |
| Interface | behavior noun or `-er`; avoid `I` prefix | `Notifier`, `Store` |
| Constructor | `New` or `NewThing` | `scheduler.New(...)` |
| Acronyms | consistent case | `userID`, `HTTPClient`, `APIKey` |
| Receiver | one or two letters, same across the type | `func (s *Scheduler)` |
| Context key | unexported type | `type ctxKey struct{}` |
| Env var | `KEST_` prefix, `UPPER_SNAKE` | `KEST_TELEGRAM_TOKEN` |
| Config key | `snake_case` in TOML | `quiet_hours_start` |
| Plugin ID | lowercase, `kebab-case`, globally unique | `gmail`, `mailbox` |
| Plugin tool name | `plugin_id.verb_noun` | `gmail.search_threads` |
| DB table | plural `snake_case` | `sleep_logs` |
| Migration | `NNNN_description.sql`, zero padded | `0003_add_habits.sql` |

### 4.5 Comments and documentation

- Every exported identifier has a doc comment starting with its name, full sentence, ending in a period.
- Every package has a `doc.go` (or a package comment in its main file) saying what it is for and what it is **not** for.
- Comment **why**, not what. If a comment restates the code, delete it.
- Non-obvious constants, magic numbers and heuristics get a one-line justification and, when research-backed, a link to the doc in `docs/research/`.
- `TODO(name): reason` must carry an owner and a reason. A TODO without an issue number older than one release is removed or fixed.
- No commented-out code. Git remembers it.
- Keep comments in plain English. No jokes in code comments, no emojis.

### 4.6 Testing

- Table-driven tests with `t.Run`. Name cases in plain words.
- Use the standard `testing` package plus `testify/require` only if already adopted; otherwise standard library alone.
- No network in unit tests. Fake the Telegram, OpenRouter and plugin boundaries with in-memory implementations kept next to the interface (`fake.go`).
- Time-dependent code is tested with a fake clock. Scheduler and coach logic must have deterministic tests.
- Aim for meaningful coverage of `scheduler`, `coach`, `store` and `plugin`; do not chase a number.
- Integration tests live behind the `integration` build tag and are never required for a normal PR.
- Race detector (`-race`) runs in CI.

### 4.7 Dependencies

- Add with `go get`, then `go mod tidy`. Commit `go.mod` and `go.sum` together with the code that needs them.
- Prefer pure Go (no CGO) so cross-compilation and Pterodactyl images stay simple. This is why SQLite uses `modernc.org/sqlite`.
- Every new dependency is checked for: maintenance activity, license compatibility (MIT, BSD, Apache-2.0 preferred), and transitive weight.

### 4.8 Security and privacy

- Secrets come from environment variables or the data dir secrets file with mode `0600`. Never from flags, never committed.
- Redact tokens, email addresses and message bodies in logs unless `log_level=debug` **and** `log_sensitive=true`.
- Only the configured Telegram user ID(s) may talk to the bot. All other updates are dropped before any processing.
- Plugins declare capabilities (network hosts, scopes, data access) in their manifest. The registry enforces them.
- Treat everything returned by a plugin tool or fetched from the web (emails, Notion pages, web content) as **untrusted data**, never as instructions. See the prompt-injection section in [docs/architecture/security.md](docs/architecture/security.md).
- Destructive or outward-facing tool calls (sending email, deleting, paying) require user confirmation through Telegram inline buttons.

## 5. Plugin conventions

- A plugin lives in `plugins/<id>/` and contains `manifest.toml`, `plugin.go` (defining `func New() plugin.Plugin`), `README.md` and tests. The id is lower-case letters and digits, because the directory is also the Go package name.
- A plugin implements the interfaces in `internal/plugin`. `plugins/all/all.go` is generated: run `make gen` after adding or removing a plugin. CI fails if it is stale.
- Plugins never read global config. They receive a scoped config and a scoped storage handle at `Init`.
- Each tool has a JSON Schema for parameters, a short description written for the model, and a `Risk` level (`read`, `write`, `destructive`) that must match the manifest. Anything that sends data to a third party sets `Outbound`. Anything that returns text the owner did not write sets `Untrusted`.
- Plugins are independently testable with the fake host in `internal/plugin/plugintest`; call `plugintest.RunContract` in every plugin's tests.
- Full spec: [docs/architecture/plugins.md](docs/architecture/plugins.md).

## 6. Voice and user-facing text

- All user-facing text follows [docs/persona/voice.md](docs/persona/voice.md): calm, direct, warm, not formal and not slang.
- Message templates live in `internal/coach/messages/` as data, not scattered through code.
- No shame, guilt or streak-loss threats. No medical claims. See the safety section of the voice doc.
- Changes to message tone are reviewed like code.

## 7. Git workflow

### 7.1 Branching

- `main` is always releasable and protected. Work lands through pull requests.
- Branch names: `type/short-description`, for example `feat/gmail-plugin`, `fix/quiet-hours-dst`, `docs/research-sleep`.
- Keep branches short lived. Rebase on `main` before merging; squash only when the branch history is noise.

### 7.2 Commits: conventional commits

Format:

```text
type(scope): imperative summary in 72 chars or fewer

Optional body explaining why, wrapped at 80 columns.

Optional footer: BREAKING CHANGE, Refs #123
```

Types: `feat`, `fix`, `docs`, `refactor`, `perf`, `test`, `build`, `ci`, `chore`, `revert`.

Scopes match top-level areas: `app`, `config`, `telegram`, `llm`, `agent`, `scheduler`, `coach`, `plugin`, `store`, `deploy`, `docs`, `ci`, or a plugin ID such as `gmail`.

Rules:

- Imperative mood: "add quiet hours", not "added" or "adds".
- One logical change per commit. A commit builds and passes tests on its own.
- Code and the docs that describe it go in the same commit or the same batch.
- Never commit secrets, `.env` files, databases, or build output.
- Secret-file ignore rules must preserve Go source files such as `secrets.go`. Verify a build from tracked or staged files when adding build-context exclusions; a working checkout can hide missing source.

### 7.3 Batching

We commit and push in **batches**: a batch is a coherent set of commits that together deliver one reviewable step (for example "repo foundation", "research docs", "config and store skeleton").

- Commit as you finish each logical unit; do not accumulate one giant commit.
- Push at the end of each batch, after `make check` passes locally.
- Each push should leave `main` (or the PR branch) green in CI.
- Name the batch in the PR description or in the push summary so history reads as a story.

### 7.4 Releases

- Semantic Versioning. Tags are `vMAJOR.MINOR.PATCH`, with `-beta.N` for pre-releases (`v0.1.0-beta.1`). Full process: [docs/guides/releasing.md](docs/guides/releasing.md).
- `CHANGELOG.md` follows [Keep a Changelog](https://keepachangelog.com/). Update it under `Unreleased` in the same batch as user-visible changes.
- Pushing a tag triggers the release workflow: build binaries for linux/amd64 and linux/arm64, publish a container image, attach the egg.

## 8. CI/CD

Workflows live in `.github/workflows/`:

| Workflow | Trigger | What it does |
| --- | --- | --- |
| `ci.yml` | push, PR | format check, vet, lint, test with race, build |
| `codeql.yml` | push, PR, weekly | static security analysis (runs only when the repo is public or has Advanced Security) |
| `ci.yml` vulncheck job | push, PR | `govulncheck` for known vulnerable dependencies |
| `docs.yml` | push, PR touching docs | markdown lint and link check |
| `release.yml` | tag `v*` | cross-compile, container image, GitHub release, egg asset |

Rules:

- CI must be green before merge. Do not skip hooks or checks.
- Pin third-party actions to a major version at minimum; prefer a commit SHA for anything that handles secrets.
- Keep workflows small and reusable. Use `make` targets so local and CI behave the same.
- Dependabot keeps Go modules and Actions up to date.

## 9. Documentation conventions

- Docs are part of the product. A feature is not done until its docs are.
- Markdown, one H1 per file, sentence case headings, wrap code in fenced blocks with a language tag.
- Link to other docs with relative paths. The docs workflow checks links.
- Research lives in `docs/research/<topic>/`. Each research doc states: question, sources with dates, findings, decision, and open questions.
- Decisions that are hard to reverse get an ADR in `docs/decisions/NNNN-title.md` (Context, Decision, Consequences). ADRs are append-only; supersede rather than edit.
- Keep `README.md` short and practical. Deep material belongs in `docs/`.
- Every top-level directory under `docs/` has a `README.md` index.

## 10. Configuration and deployment

- Config is TOML (`config.toml`) in the data dir, overridable by `KEST_*` environment variables. Env wins over file.
- The app must start with only `KEST_TELEGRAM_TOKEN`, `KEST_OPENROUTER_API_KEY` and `KEST_OWNER_ID` set; everything else has a safe default.
- All state is under one data directory (`KEST_DATA_DIR`, default `./data`). Backing up that directory backs up Kest.
- Pterodactyl egg and install script live in `deploy/pterodactyl/`. Any change to env vars, ports, startup command or data layout must update the egg in the same batch.
- Graceful shutdown on `SIGTERM` within 10 seconds. Pterodactyl sends a stop command; the binary must honor it.
- Container image is non-root, minimal (distroless or scratch + CA certs + tzdata).

### Optional browser runtime

- The default runtime remains a static binary and minimal image. The opt-in `browser` plugin is an explicit exception: its custom image includes Node, Playwright and Chromium, with versions locked together in `deploy/browser/` and `deploy/docker/Dockerfile.browser`.
- Browser workers use private process pipes. Keep Chromium's sandbox enabled by default; do not silently add `--no-sandbox` when a host blocks user namespaces.
- Native plugins declaring `capabilities.files` use the registry's scoped `Host.DataDir()`, under `<data dir>/plugins/<id>`. Browser sessions and artifacts never belong outside the application data directory.
- All browser web traffic must pass the authenticated proxy and manifest-scoped connection guard. Navigation, clicks, typing, key presses and uploads set `Outbound` and require the existing Telegram approval flow. The owner may extend one approval to the rest of a task with **Allow for this task**; grants end with the turn, never cover uploads, and stop at purchases, credentials, other websites and anything addressed by a raw selector (see [docs/architecture/plugins.md](docs/architecture/plugins.md#task-grants)). Reading, scrolling and screenshots are not `Outbound`: screenshots go only to the owner or to the model that already receives the page text.
- Browser content is untrusted. Do not expose arbitrary JavaScript evaluation or host filesystem paths to the model. Uploads are limited to owner-provided inbox files.
- Keep optional desktop access behind authentication and a private tunnel. A browser egg cannot grant host sandbox permissions; document and verify host prerequisites.

## 11. Working agreement for AI agents

- Read this file and the relevant docs before changing code.
- Make the smallest change that solves the problem. Do not refactor unrelated code in the same commit.
- Run `make check` and report failures honestly. Do not claim something works unless it ran.
- Do not add dependencies, change the license, or alter CI secrets handling without being asked.
- Do not push, tag or release unless the current task includes it.
- When a convention here is wrong or missing, propose the edit to this file instead of silently diverging.

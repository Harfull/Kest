# Repository file structure

The full target layout. Items marked **(P1)** to **(P6)** are created in the roadmap phase of the same number; see [../roadmap.md](../roadmap.md). Unmarked items belong to Phase 0 (foundation) and exist in the repository, except `LICENSE`, which is waiting on the owner's choice.

```text
Kest/
├── AGENTS.md                         repository conventions
├── README.md                         short overview and quick start
├── CHANGELOG.md                      Keep a Changelog
├── LICENSE                           (owner to choose)
├── Makefile                          build, lint, test, check
├── go.mod / go.sum
├── .env.example                      every KEST_* variable, documented
├── config.example.toml               full config with defaults and comments
├── .editorconfig  .gitattributes  .gitignore
├── .golangci.yml  .markdownlint-cli2.yaml
│
├── .github/
│   ├── workflows/{ci,codeql,docs,release}.yml
│   ├── dependabot.yml
│   ├── pull_request_template.md
│   └── ISSUE_TEMPLATE/{bug_report,feature_request}.md
│
├── cmd/
│   └── kest/
│       └── main.go                   flags, build info, app.Run; embeds time/tzdata
│
├── internal/
│   ├── version/                      Version and Commit set via -ldflags
│   ├── clock/                        Clock interface, real and fake clocks
│   ├── logging/                      slog setup and redaction handler
│   ├── config/                       defaults, TOML + env loading, validation
│   ├── secrets/                      0600 secrets file, token store         (P1)
│   ├── audit/                        audit log writer                       (P1)
│   │
│   ├── app/                          wiring and lifecycle
│   │   ├── app.go                    Run(ctx): build graph, start, wait, stop
│   │   ├── chat.go                   commands, text and voice turns, confirmations
│   │   ├── maintenance.go            nightly backup and pruning
│   │   └── messages.go               user-facing strings
│   │
│   ├── store/                        SQLite
│   │   ├── sqlite.go                 open, pragmas, verification, pools
│   │   ├── migrate.go                embedded migration runner
│   │   ├── migrations/               0001_init.sql, 0002_...
│   │   └── *_repo.go                 one repository per aggregate
│   │
│   ├── telegram/                     transport adapter
│   │   ├── transport.go              interfaces: Inbox, Outbox, Update, Reply
│   │   ├── bot.go                    go-telegram/bot implementation
│   │   ├── ownerguard.go             drop non-owner updates
│   │   ├── format.go                 HTML escaping and 4096-char splitting
│   │   ├── keyboard.go               inline buttons, callback IDs
│   │   ├── voice.go                  voice download, sendVoice
│   │   └── fake.go                   in-memory transport for tests
│   │
│   ├── llm/                          OpenRouter
│   │   ├── client.go                 HTTP client, auth, headers, retry
│   │   ├── chat.go                   chat completions, streaming
│   │   ├── tools.go                  tool schema mapping and call parsing
│   │   ├── audio.go                  transcription and speech
│   │   ├── models.go                 model slots, fallbacks
│   │   ├── usage.go                  cost accounting, budget guard
│   │   └── fake.go                   scripted model for tests
│   │
│   ├── agent/                        conversation engine
│   │   ├── agent.go                  prompt assembly and the model/tool loop with caps
│   │   ├── policy.go                 risk policy and taint rule
│   │   ├── untrusted.go              wrapping and sanitizing external content
│   │   ├── confirm.go                pending approvals and the button flow
│   │   ├── memory.go                 remember/recall/forget tools, prompt context, summaries
│   │   ├── voice.go                  machine-checkable subset of the voice guide
│   │   ├── persona.txt               embedded persona prompt (kept equal to the docs)
│   │   └── eval_test.go              opt-in model evaluation (build tag eval)
│   │
│   ├── scheduler/                    persistent jobs
│   │   ├── scheduler.go              loop, claiming, workers
│   │   ├── job.go                    job model and states
│   │   ├── recurrence.go             daily, weekdays, weekly, every, cron
│   │   ├── dst.go                    wall-clock to UTC rules
│   │   └── store.go                  job persistence
│   │
│   ├── coach/                        coaching engine                        (P3)
│   │   ├── coach.go                  module registry, event handling
│   │   ├── module.go                 Module interface
│   │   ├── habit/                    habits, if-then plans, logs
│   │   ├── consistency/              rolling adherence, "never miss twice"
│   │   ├── checkin/                  morning, midday, evening, weekly review
│   │   ├── sleep/                    events, inference, quiet hours         (P3)
│   │   ├── workout/                  plans, logging, recovery               (P4)
│   │   ├── nutrition/                meal logging, gentle targets           (P4)
│   │   ├── focus/                    tasks, procrastination support         (P4)
│   │   ├── lapse/                    miss handling protocol
│   │   ├── budget/                   notification budget and cooldowns
│   │   ├── adapt/                    timing adaptation from responsiveness  (P5)
│   │   ├── experiments/              message variants, consent-based tests  (P5)
│   │   └── messages/                 templates as data, by tone and type
│   │
│   └── plugin/                       plugin framework
│       ├── plugin.go                 Plugin, Tool, Risk, JobProvider
│       ├── manifest.go               manifest, typed config, validation
│       ├── registry.go               loading, manifest/code checks, tool lookup, host
│       ├── host.go                   Host, KV, Jobs, Notification
│       ├── httpclient.go             allowlisted, SSRF-safe HTTP client
│       ├── declarative/              TOML-defined HTTP plugins              (P5)
│       └── plugintest/               fake host, deps and contract check
│
├── plugins/                          built-in integrations
│   ├── all/all.go                    generated by scripts/genplugins
│   ├── reminders/                    one-off and recurring reminders
│   ├── tasks/                        simple task list
│   ├── webfetch/                     fetch URLs as untrusted text
│   ├── gmail/                        Gmail API                              (P4)
│   ├── mailbox/                      generic SMTP and IMAP                  (P4)
│   ├── notion/                       Notion                                 (P4)
│   ├── calendar/                     calendar read and write                (P5)
│   └── mcp/                          MCP client, tool bridging              (P5)
│       Each plugin directory: manifest.toml, plugin.go, README.md, tests.
│
├── pkg/
│   └── kestsdk/                      public plugin SDK, once stable         (P6)
│
├── deploy/
│   ├── pterodactyl/
│   │   ├── egg-kest.json             PTDL_v2 egg
│   │   ├── install.sh                the installation script, mirrored inside the egg
│   │   └── README.md
│   └── docker/
│       ├── Dockerfile                multi-stage, non-root
│       └── compose.yml
│
├── scripts/
│   ├── genplugins/                   regenerates plugins/all
│   ├── sync-egg/                     generates the egg from install.sh
│   └── check-docs/                   local link and index checks            (P1)
│
├── testdata/                         golden files, recorded API responses (scrubbed)
│
└── docs/
    ├── README.md
    ├── roadmap.md
    ├── architecture/                 this folder
    ├── research/                     one folder per topic, with sources
    ├── decisions/                    ADRs
    ├── persona/                      voice and message guidelines
    ├── deployment/                   Pterodactyl and Docker guides
    └── guides/                       configuration, plugin authoring, operations
```

## Rules enforced by the layout

- Anything under `internal/` is private to this module.
- `plugins/` imports only `internal/plugin` (and later `pkg/kestsdk`). A lint rule (`depguard`) enforces this once the plugin framework lands.
- Generated files carry a `// Code generated ... DO NOT EDIT.` header and are verified in CI by running the generator and checking for a clean diff.
- Every directory under `docs/` has a `README.md` index.

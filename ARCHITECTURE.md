# Architecture

> For Korean version, see [ARCHITECTURE_KO.md](ARCHITECTURE_KO.md).

## Design Principles

1. **All AI interactions go through CLI subprocesses** — no direct API calls, no API key management
2. **Daemon owns all business logic** — CLI, Web UI, and MCP are pure presentation layers
3. **One sheep per project** — each project gets a dedicated Claude Code worker with persistent session
4. **Single binary deployment** — Web UI is embedded via `go:embed`

## System Overview

```
┌─────────────────────────────────────────────────────────────┐
│                     Shepherd Daemon                          │
│                    (shepherd serve)                           │
│                                                              │
│  ┌──────────┐  ┌───────────┐  ┌──────────┐  ┌───────────┐ │
│  │  Fiber   │  │  Queue    │  │  Auth    │  │ Scheduler │ │
│  │ REST API │  │ Processor │  │ JWT+     │  │ Cron/     │ │
│  │ + SSE    │  │           │  │ bcrypt   │  │ Interval  │ │
│  └────┬─────┘  └─────┬─────┘  └──────────┘  └───────────┘ │
│       │              │                                       │
│  ┌────┴──────────────┴──────┐                               │
│  │       SSE Event Hub       │                               │
│  │  (real-time broadcast)    │                               │
│  └────┬──────────────┬──────┘                               │
│       │              │                                       │
│  ┌────┴────┐   ┌─────┴──────┐   ┌───────────┐             │
│  │ Worker  │   │  SQLite    │   │  Skill    │             │
│  │ Manager │   │ (Ent ORM)  │   │  Engine   │             │
│  └─────────┘   └────────────┘   └───────────┘             │
└──────┬──────────────┬──────────────┬────────────────────────┘
       │              │              │
┌──────┴──┐   ┌──────┴──────┐  ┌───┴────────┐
│  Svelte │   │ Interactive │  │    MCP     │
│  WebUI  │   │  CLI client │  │   Server   │
│(browser)│   │ (terminal)  │  │  (stdio)   │
└─────────┘   └─────────────┘  └────────────┘
```

## Data Flow

### Task Execution

```
1. User submits task
   ├── CLI:   shepherd "Add login feature" → POST /api/embedded/run (embedded run in cwd)
   ├── WebUI: POST /api/tasks (project screen) or POST /api/shepherd/tasks (shepherd console)
   └── MCP:   task_start tool call

2. Task is queued
   └── Queue Processor picks up pending tasks and assigns each to a sheep
       (project tasks → the project's sheep; shepherd tasks → the manager sheep)

3. Worker executes
   ├── Claude Code CLI: claude --print --resume <session_id> -p <prompt>
   ├── Or OpenCode CLI: opencode --resume <session_id> -p <prompt>
   └── Output is streamed in real-time via SSE

4. Results recorded
   ├── Task status: running → completed/failed
   ├── Summary, modified files, error (if any)
   └── Session ID preserved for conversation continuity
```

### Direct Embedded Run (CLI)

`shepherd` and `shepherd "..."` take a shortcut that skips steps 2–3 above: the
CLI sends `{project_path: cwd, prompt}` to `POST /api/embedded/run`. The daemon
runs its already-wired embedded executor in that directory and streams the
output back as request-scoped SSE (never through the global `/api/events` hub).
No project registration, sheep assignment, or queue task is involved.

### Authentication Flow

```
POST /api/auth/login {username, password}
  → bcrypt verify against config hash
  → Issue JWT access token (24h) + refresh token (7d)
  → Client stores tokens, sends Bearer header on each request

AuthMiddleware:
  → Extract Bearer token from Authorization header
  → Verify JWT signature and expiration
  → Reject if JWT secret is empty (security: no anonymous fallback)
```

## Package Architecture

```
cmd/shepherd/
└── main.go              # CLI entrypoint (~2000 lines)
                         # All Cobra commands defined here

ent/
└── schema/              # Database entities
    ├── sheep.go         # id, name, status, session_id, provider, project_id
    ├── project.go       # id, name, path, description
    ├── task.go          # id, prompt, status, summary, files_modified, error
    ├── skill.go         # id, name, content, tags, project_id (nullable)
    └── schedule.go      # id, type, expression, prompt, enabled, project_id

internal/
├── agent/               # AI provider abstraction
│   ├── provider.go      # AgentProvider interface
│   ├── claude.go        # Claude Code CLI wrapper
│   ├── opencode.go      # OpenCode CLI wrapper
│   └── router.go        # Auto-select logic based on prompt analysis
│
├── browser/             # Browser automation (Rod)
│   ├── manager.go       # Browser lifecycle (launch, connect, close)
│   ├── session.go       # Per-sheep session management, multi-page
│   └── actions.go       # 20+ actions (click, type, screenshot, etc.)
│
├── checkpoint/          # Working-tree snapshots via hidden git refs
│   └── checkpoint.go    # Probe, Take, Changes, Transplant, task refs
│
├── config/              # Viper-based YAML configuration
│   └── config.go        # Defaults, load, get/set, file path
│
├── daemon/              # Daemon process management
│   └── daemon.go        # PID file, signal handling, start/stop/status
│
├── db/                  # SQLite database
│   └── db.go            # Ent client initialization
│
├── i18n/                # Internationalization
│   └── i18n.go          # Messages struct with ko/en, T() accessor
│
├── manager/             # Task analysis & routing
│   └── manager.go       # Claude CLI-based intent analysis with JSON schema
│
├── mcp/                 # MCP server (JSON-RPC 2.0 over stdio)
│   └── server.go        # Tool registration, request handling
│
├── names/               # Sheep name management
│   └── names.go         # Default pool + custom names CRUD
│
├── project/             # Project management
│   └── project.go       # Add, remove, list, assign sheep (1:1 mapping)
│
├── queue/               # Task lifecycle
│   └── queue.go         # Create, process, complete, fail, list
│
├── scheduler/           # Scheduled task execution
│   └── scheduler.go     # Cron parser, interval timer, auto-task creation
│
├── server/              # HTTP server (Fiber)
│   ├── server.go        # App init, route registration, middleware
│   ├── auth.go          # JWT issue/verify, bcrypt, login/refresh
│   ├── middleware.go     # Auth middleware, CORS
│   ├── sse.go           # SSE Event Hub (broadcast to all clients)
│   ├── handlers_sheep.go
│   ├── handlers_project.go
│   ├── handlers_task.go
│   ├── handlers_system.go
│   ├── handlers_git.go       # Read-only git operations
│   ├── handlers_docs.go      # Project markdown viewer
│   ├── handlers_skill.go
│   ├── handlers_schedule.go
│   └── handlers_upload.go
│
├── skill/               # Skill system
│   └── skill.go         # CRUD, bundled skills, import/export
│
└── worker/              # Sheep execution
    ├── worker.go        # CRUD, status management
    └── interactive.go   # CLI subprocess execution, output parsing

web/                     # Svelte SPA (JavaScript only, no TypeScript)
├── src/
│   ├── lib/
│   │   ├── api.js       # REST client with auto Bearer token
│   │   ├── sse.js       # SSE connection with auto-reconnect
│   │   └── stores.js    # Svelte stores (auth, sheep, tasks, etc.)
│   └── routes/          # SvelteKit file-based routing
│       ├── +page.svelte           # Dashboard
│       ├── login/+page.svelte     # Login
│       ├── sheep/+page.svelte     # Sheep management
│       ├── projects/+page.svelte  # Project list
│       ├── projects/[name]/       # Project detail (git, docs, schedules)
│       ├── tasks/+page.svelte     # Task list
│       ├── tasks/[id]/            # Task detail
│       ├── schedules/+page.svelte # Schedule management
│       ├── skills/+page.svelte    # Skill management
│       └── settings/+page.svelte  # Settings
└── build/               # Production build → embedded via go:embed
```

## Task Checkpoints

`internal/checkpoint` snapshots a project's working tree with git plumbing only. It imports `internal/procutil` and the standard library, never `ent`, `queue`, or `server`, so the queue and API can share it without an import cycle.

- **Refs**: `refs/shepherd/tasks/<id>/start`, `end`, `pre-revert`, `reverted`. Only this prefix is listed or deleted, so a caller bug cannot touch user branches or tags.
- **Temporary index**: each snapshot copies the real index (preserving mtime) to `shepherd-ckpt-index-<random>` under the common git dir and runs `git add -A` with `GIT_INDEX_FILE`. The user's real index is never modified, and the temporary index plus its lock file are removed on both success and failure. Repositories without commits get a parentless snapshot commit.
- **Size limits**: 20,000 untracked files, 50 MB per file, 200 MB total. Oversized trees return `ErrTooLarge` and create no ref. Dropping only the large files would make the start and end snapshots cover different sets and produce fake additions/deletions.
- **Diff and restore**: diffs always use `--no-color --no-ext-diff --no-textconv --src-prefix=a/ --dst-prefix=b/` so user git config cannot break parsing. Restores use `git merge-tree --write-tree` for a 3-way merge and `git apply` (no `--index`) so the user's index stays untouched. Conflicts return the file list and leave the working tree unchanged.
- **Process safety**: `Take` and `Transplant` serialize per repository top level with a process-local mutex. All git invocations run with `LC_ALL=C`, because merge-tree conflict text is parsed. Author and committer are fixed to `shepherd <shepherd@localhost>`.
- **Retention**: `task_checkpoint_retention_days` (default 30, `0` disables) drives a pruner that runs once after daemon startup and then daily. It groups refs by task ID, deletes refs for tasks missing from the DB or older than the cutoff (`completed_at`, or `reverted_at` after a revert), and logs `[checkpoint] pruned N refs in <dir>`. Snapshot objects are left to git's default gc (unreachable after two weeks). Deleting a task removes its refs first; a failure there is logged and retried by the pruner.
- **Known limits**: changes made by other processes during a task are included in its snapshot, because the snapshot is the whole working tree. If the daemon dies mid-task there is no end snapshot, and the recovered task records `checkpoint_error` instead of a silent partial diff. Export/import carries checkpoint metadata but not the refs, so a restore elsewhere has no diff history.
- **Task deletion**: `queue.DeleteTask` deletes checkpoint refs before removing the row. The failure is logged and does not block deletion.

## Entity Relationships

```
Sheep 1:1 Project       # Each sheep is assigned to exactly one project
Project 1:N Task        # A project has many tasks
Sheep 1:N Task          # A sheep executes many tasks
Project 1:N Schedule    # A project has many schedules
Project 0:N Skill       # A project can have project-scoped skills
(global) 0:N Skill      # Skills with no project_id are global
```

## Key Technical Decisions

| Decision | Rationale |
|----------|-----------|
| CLI subprocess for AI | No API key management; Claude Code handles auth |
| SQLite + Ent ORM | Type-safe, auto-generated, no external DB dependency |
| Fiber HTTP framework | Fast, Express-like API, built-in middleware |
| SSE over WebSockets | Simpler, HTTP-native, sufficient for one-way streaming |
| Svelte (JS only) | Lightweight, no TypeScript complexity |
| go:embed for WebUI | Single binary deployment, no separate static server |
| Direct embedded endpoint | CLI `shepherd "..."` runs the agent in cwd via `/api/embedded/run`, bypassing manager/sheep/queue |
| Rod for browser | Chromium DevTools Protocol, no external browser driver |
| Config-based auth | Single-user tool; no need for user table in DB |
| 1:1 sheep-project | Session continuity; each sheep maintains conversation context |

## Multi-Provider Architecture

```
AgentProvider (interface)
├── ClaudeProvider    # claude --print --output-format json
├── OpenCodeProvider  # opencode -p <prompt> --resume <session>
└── AutoRouter        # Analyzes prompt → selects best provider
                      # Falls back on rate limit
```

Provider selection per sheep: `claude` (default), `opencode`, or `auto`.

## Security Model

- **Authentication**: JWT + bcrypt, single-user config-based
- **JWT auto-generation**: 32-byte random secret on first start
- **Empty JWT rejection**: Returns 401 (no anonymous fallback)
- **CORS**: Configurable via `SHEPHERD_CORS_ORIGIN` env var
- **Git input validation**: Branch names, commit hashes, file paths sanitized
- **Path traversal protection**: Docs endpoint restricts to `.md` files within project
- **No secrets in repo**: Config file in `~/.shepherd/`, not in project directory

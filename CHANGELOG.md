# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

## [0.4.0] - 2026-11-16

### Added
- `deploy/shepherd.system.service` as an optional system-unit example, next to the default user unit
- The shell tool's default run time is now a setting: `bash_timeout` (Settings → Common → Bash Tool Timeout, or `shepherd config set bash_timeout 10m`). It replaces the hard-coded 120 seconds in the `bash`/`shell`/`powershell`/`pwsh` tool, and the default is now **600 seconds (10 minutes)** — a build or test suite that used to be killed at two minutes when the model forgot to pass `timeout` now runs. The per-call `timeout` argument still wins, but it is clamped to 3600 seconds, so a hallucinated value cannot park a turn for days. The tool schema and the timeout message report the effective number instead of a fixed 120, and the agent loop's 5-minute per-tool watchdog now follows this setting (plus a 30-second margin) so it never cuts a legitimate long command short before the shell path can report its own readable timeout. Accepts whole seconds (`300`) or a duration (`5m`); invalid values return 400 from the API and `shepherd config set`, and an unusable value in `config.yaml` falls back to 600
- Upload size is now a setting: `max_upload_mb` (Settings → Common → Max Upload Size, or `shepherd config set max_upload_mb 50`). It replaces the hard-coded 10 MB per-file limit in both upload endpoints (task attachments and the Files tab), and the HTTP body limit is derived from it, so a raised limit works without a rebuild. The daemon re-reads the value on every request — a saved change applies to the next upload, no restart. Range: 1–512 MB (the daemon buffers a whole upload in memory); an invalid value returns 400 and keeps the previous one, and `shepherd config set` validates it too. The browser reads the same number, so its "file too large" message and its image-compression target follow the setting instead of 10 MB. The transport body limit now also clears the 50 MB task-import dump at the default setting, which is what it was before. Behind a reverse proxy or ingress, raise its own body-size limit to match (for example `nginx.ingress.kubernetes.io/proxy-body-size`)

### Fixed
- A Claude task that did nothing and was still recorded as `completed`. Two causes stacked up. (1) The model list for the Claude and manager tabs offered `claude-sonnet-5.5` and `claude-opus-5.5`, which do not exist — every Claude model id uses hyphens — so the CLI printed "There's an issue with the selected model" and exited 1. The list now holds the real ids (`claude-sonnet-5-5`, `claude-opus-5-5`) plus the `fable` alias. (2) That exit was parsed as success: `executeWithStreaming` returned whatever `parseStreamOutput` produced as long as a `result` event existed, but Claude puts a refusal's whole text in that same field, so the presence of a result is not evidence of success. `parseStreamOutput` now reads the terminal event's `is_error`, `api_error_status`, `api_error_type`, and `terminal_reason` into `ExecuteResult`, and both the streaming path and the PTY path (`auto_approve=false`) turn them into an error; `classifyClaudeFailure` prefers those structured fields over raw-output search, and keeps the `rate limit:` prefix a 429 needs for provider handoff (#675). Saving is blocked too: `ValidateClaudeModelID` rejects an id with a dot or a space, and `PATCH /api/config` applies it to `model_claude` and `shepherd_model` (#12082, #884)
- A clean clone could not compile. `internal/server/web_dist` is a `go:embed` directory, and an empty directory with only ignored files makes the embed directive fail with "no matching files found". `web_dist/.gitkeep` is committed again so the embed always has one entry, and `make clean` now preserves it — `rm -rf web_dist` used to reproduce the same failure locally. A guard test asserts the file is still tracked; CI fills the directory before building, so only that test can catch the clone case (#840)
- `install.sh` left a running daemon in place when the PID file was missing, empty, or pointed at a dead or reused pid: it now falls back to `pgrep`, and it checks the command line of a live pid before sending SIGTERM, so a reused pid is never killed by accident. One shared `DAEMON_PATTERN` (`^[^ ]*shepherd (serve|serve-foreground)( |$)`) is used by detection, the stop wait, and `pkill`, so a shell process that merely mentions `shepherd serve` in the middle of its command line is no longer matched (#844)
- In an interactive Claude stream, mid-turn narration was glued onto the next tool header. `parseStreamLine` did not append a newline to discrete text events, so the two arrived on one line and the tool header lost its own line; each discrete event now ends with a newline (#11771)
- The systemd unit runs `serve-foreground` and sets a default `PATH` that includes `~/.local/bin`, so `claude` and `grok` resolve in a user-unit install. Install dir, PATH, and daemon stop/start are now the same across the unit files, `install.sh`, and the READMEs; the READMEs add the mise/asdf/nvm shim case and how to confirm which binary is running
- `CONTRIBUTING.md` stated a source-build order that produced a binary with no Web UI: the Web UI must be built before `go build`, because it is embedded at compile time
- Files-tab upload: `Unexpected token '<', "<html> <h"... is not valid JSON`. A reverse proxy or ingress in front of the daemon refuses a body over its own limit and answers with an HTML error page (nginx writes a 413 page for a body over `proxy-body-size`). The Web UI called `res.json()` on every reply, so that page became a JSON parse error that named neither the limit nor the stage that refused the file. Three layers are fixed: the browser now parses every reply defensively (`web/src/lib/apiResponse.js`) and reports a readable message with the HTTP status; the daemon answers request-level failures — including the oversized-body rejection that fasthttp raises before any handler runs — with the same JSON envelope as its handlers, and a 413 names the current `max_upload_mb` plus the reminder that the proxy enforces its own limit; and the sample ingress manifest raises `proxy-body-size` to 600m, since 50m silently capped every upload no matter what `max_upload_mb` said. The same defensive parsing now covers token refresh and login, where a proxy error page could otherwise be written into the token stores


## [0.3.0] - 2026-11-02

### Added
- PWA icons and favicon redrawn in the Pico Night Gray pixel-art style: night-sky bands, crescent moon, and a lantern staff. `npm run gen:icons` (`web/scripts/gen-icons.mjs`) draws every PNG on integer pixel grids from the sprite in `sprites.js`, with no dependencies beyond `node:zlib`. Adds `apple-touch-icon` (180), a borderless `icon-maskable-512`, and a white `badge-96` notification icon. A `.gitignore` exception (`!web/static/*.png`) fixes icons that were never committed, so CI release builds now ship the favicon and PWA icons

### Changed
- Settings → Shepherd provider list now offers `Embedded` and no longer lists `magi` (which has no single model to select). A manager sheep already saved on `magi` stays in the list; a save that would switch a provider to `magi` returns 400

### Fixed
- `task_complete` / `task_error` now guard the target: a call whose `task_id` differs from the caller's own task is rejected, and a target that is not `pending`/`running` is rejected, so a finished task's summary, error, and `completed_at` are no longer overwritten. The embedded and interactive prompts now inject the current task id in a `[Current Task]` section so the sheep no longer guesses it from `get_status`/`get_history` (#722)
- A stream error whose message says the engine "restarts it on the next request" (Strata `EngineDied`: verify timeout, OOM kill) is now classified as transient and retried, instead of failing the whole task. The retry count stays bounded (at most 6 sends), and the partial answer of a dropped attempt is not kept

## [0.2.0] - 2026-10-30

### Added
- Prebuilt release binaries for Linux, macOS, and Windows (amd64 and arm64), published by the `Release` workflow when a `v*` tag is pushed
- Rate limit handoff: when a task hits a rate limit, a follow-up task continues on the provider and model set in `limit_handoff_provider` / `limit_handoff_model` (selectable in the Claude settings screen) instead of waiting for the backoff retries. When no target is set, the backoff retry behavior does not change
- Task checkpoints: hidden git refs capture the working tree before and after each task, with exact per-file diffs, 3-way revert/undo, API/CLI/web support, configurable retention (`task_checkpoint_retention_days`), and ref cleanup on task deletion
- Skill system: file-based skills with WebUI management, bundled default skills, prompt injection during task execution
- Scheduling system: cron and interval-based task scheduling
- Docs tab in WebUI: project documentation viewer integrated with the server
- Git integration: branch, commit history, and diff viewing in WebUI
- Browser automation: Rod-based browser control with 20+ MCP tools
- MCP server mode for integration with Claude Desktop and other MCP clients
- Multi-provider support: Claude Code, OpenCode, and auto-select
- CONTRIBUTING.md and CHANGELOG.md for open-source readiness
- Direct embedded endpoint `POST /api/embedded/run`: `cd <project> && shepherd` or `shepherd "..."` runs the embedded coding agent in the current directory through the daemon (request-scoped SSE, no project/sheep/queue)
- Task-chat image attachments are compressed in the browser before upload: JPEG quality 90 with the longest side capped at 4096px, then stepwise downscale if a result is still over the server's 10 MB limit. Files that would grow (already-optimized JPEGs, tiny icons) keep their original bytes; the attachment chip shows the before/after sizes

### Changed
- All internal error messages and comments converted to English
- README rewritten in English
- README and README_KO rewritten around the Web UI (shepherd, task changes, MAGI, wiki) with new Pico Night Gray screenshots; the CLI, REST, and full config references were removed
- Internal constants (`ShepherdName`, `ManagerName`) changed from Korean to English
- Documentation IPs and domains replaced with example placeholders
- CLI messages internationalized via i18n package (English and Korean)
- CLI default paths (`shepherd`, `shepherd "..."`, `shepherd task "..."`) are now thin clients for the daemon's embedded agent

### Removed
- Bubbletea TUI (`internal/tui/`) and the `shepherd tui` command
- `charmbracelet/bubbletea`, `bubbles`, and `lipgloss` dependencies

### Security
- JWT secret auto-generated on server startup if not configured
- Empty JWT secret now returns 401 instead of allowing anonymous access
- CORS origin configurable via `SHEPHERD_CORS_ORIGIN` environment variable (no longer hardcoded wildcard)
- Input validation added for git branch names, commit hashes, and file paths
- Build artifacts removed from git tracking

## [0.1.0] - 2025-01-01

### Added
- Initial release
- Shepherd manager: task analysis and routing via Claude Code CLI
- Sheep workers: individual Claude Code instances per project
- Project management: add, remove, assign sheep
- Task queue: pending → running → completed/failed lifecycle
- SQLite database with Ent ORM
- Bubbletea TUI with split-view and real-time streaming
- Web UI: Svelte SPA with SSE real-time updates
- Authentication: JWT + bcrypt, config-based single-user
- Ingress Nginx reverse proxy configuration
- Korean sheep name pool with custom name support
- Graceful shutdown and auto-recovery

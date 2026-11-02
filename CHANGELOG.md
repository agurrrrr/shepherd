# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

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

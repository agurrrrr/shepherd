# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added
- Skill system: file-based skills with WebUI management, bundled default skills, prompt injection during task execution
- Scheduling system: cron and interval-based task scheduling
- Docs tab in WebUI: project documentation viewer integrated with the server
- Git integration: branch, commit history, and diff viewing in WebUI
- Browser automation: Rod-based browser control with 20+ MCP tools
- MCP server mode for integration with Claude Desktop and other MCP clients
- Multi-provider support: Claude Code, OpenCode, and auto-select
- CONTRIBUTING.md and CHANGELOG.md for open-source readiness

### Changed
- All internal error messages and comments converted to English
- README rewritten in English
- Internal constants (`ShepherdName`, `ManagerName`) changed from Korean to English
- Documentation IPs and domains replaced with example placeholders
- CLI messages internationalized via i18n package (English and Korean)

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

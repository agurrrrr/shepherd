# Contributing to Shepherd

Thank you for your interest in contributing to Shepherd! This guide will help you get started.

## Development Setup

### Prerequisites

- **Go 1.21+**
- **Node.js 18+** (for the Web UI)
- **Claude Code CLI** installed and in `PATH` (for testing task execution)

### Building from Source

```bash
# Clone the repository
git clone https://github.com/agurrrrr/shepherd.git
cd shepherd

# Build the CLI
go build -o shepherd ./cmd/shepherd

# Build the Web UI (required for embedded frontend)
cd web && npm install && npm run build && cd ..

# Run tests
go test ./...
```

### Project Structure

```
shepherd/
├── cmd/shepherd/          # CLI entrypoint (main.go)
├── ent/                   # Ent ORM (auto-generated)
│   └── schema/            # Entity definitions
├── internal/
│   ├── agent/             # AI provider abstraction
│   ├── browser/           # Browser automation (Rod)
│   ├── config/            # YAML configuration
│   ├── db/                # SQLite database
│   ├── i18n/              # Internationalization (en, ko)
│   ├── manager/           # Task analysis & routing
│   ├── mcp/               # MCP server
│   ├── names/             # Sheep name pool
│   ├── project/           # Project CRUD
│   ├── queue/             # Task lifecycle
│   ├── scheduler/         # Cron & interval scheduling
│   ├── server/            # HTTP server + SSE + WebUI
│   ├── skill/             # Skill system
│   ├── tui/               # Terminal UI
│   └── worker/            # Sheep execution
└── web/                   # Svelte WebUI (SPA)
```

## How to Contribute

### Reporting Issues

- Search existing issues before creating a new one
- Include steps to reproduce, expected behavior, and actual behavior
- Include your Go version, OS, and Shepherd version

### Submitting Changes

1. Fork the repository
2. Create a feature branch: `git checkout -b feature/my-feature`
3. Make your changes
4. Ensure tests pass: `go test ./...`
5. Ensure the build succeeds: `go build ./...`
6. Commit with a clear message
7. Push to your fork and open a Pull Request

### Code Guidelines

- Follow standard Go conventions (`gofmt`, `go vet`)
- All error messages must be in English
- All user-facing strings should use the `i18n` package for localization
- The Web UI uses **pure JavaScript** — no TypeScript
- Keep changes focused and minimal

### Ent Schema Changes

If you modify files in `ent/schema/`, regenerate the ORM code:

```bash
go generate ./ent
```

### Web UI Development

```bash
cd web
npm install
npm run dev    # Development server with hot reload
npm run build  # Production build (output goes to build/)
```

The built frontend is embedded into the Go binary via `go:embed`.

## Code of Conduct

Be respectful and constructive in all interactions. We welcome contributors of all experience levels.

## License

By contributing, you agree that your contributions will be licensed under the [MIT License](LICENSE).

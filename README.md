# Shepherd

A self-hosted control room for a flock of AI coding agents. Each project gets its own agent (a "sheep"), and a manager agent (the "shepherd") hands out work, watches it, and reviews the results. You drive all of it from one Web UI.

> 한국어 문서는 [README_KO.md](README_KO.md)를 참고하세요.

![Shepherd dashboard](assets/webui-dashboard.png)

## Highlights

- **One sheep per project.** Sheep run in parallel, each in its own repository, with live streamed output.
- **A shepherd that manages the flock.** You tell the shepherd what you want. It writes issues, delegates them to project sheep, wakes up when the work finishes, and reviews the result.
- **Every change is reversible.** Shepherd snapshots the working tree before and after each task, so you can see exactly what a task changed and revert it.
- **Many AI backends.** Claude Code, OpenCode, Pi, and Grok CLIs, local or hosted models through the built-in agent, and MAGI, a three-model consensus engine.
- **Project memory.** Each project has an issue tracker and a wiki that the agents read and update.
- **Runs on your machine.** A single Go binary with the Web UI embedded. Installable as a PWA with push notifications.

## The Web UI

### Dashboard

The dashboard shows the whole flock at a glance: which sheep are working or resting, live output from every running task, token usage by project and model (5 hours to 1 year), and recent tasks. You can send extra instructions to a running sheep from its card. The monitor view puts all running tasks side by side.

### Shepherd

The shepherd is a manager agent with no project of its own. It never edits code. It delegates through issues:

1. You give it an instruction in the console.
2. It creates or picks issues and runs them on the right project sheep, with **watch** turned on.
3. When a watched task finishes, the shepherd wakes up and reviews it. It closes the issue, re-runs it with more guidance, or opens a follow-up issue.
4. A periodic **patrol** checks all projects with plain code (no LLM) and wakes the shepherd only for new problems, such as failed or stuck tasks.

![Shepherd console](assets/webui-shepherd-console.png)

The shepherd screen also has its task history, the patrol log, a **cross-project issue board**, and a **needs-attention** list for things that need your decision. Autonomy tiers (report, plan, execute, full) and daily caps on wakes, active delegations, and cost keep it under control. You set all of this in **Settings → 목자 (Shepherd)**.

![Cross-project issue board](assets/webui-issue-board.png)

### Project workspace

Each project has a workspace with these tabs: **Live Output**, **Task History**, **Files**, **Git**, **Schedules**, **Skills**, **Issues**, **Wiki**, and **Settings**. From Live Output you send tasks, add instructions to a running task, or stop it. You can change the sheep's provider and model in the header.

![Project live output](assets/webui-project-output.png)

- **Files**: browse and download project files.
- **Git**: commit graph, branches, and diffs. Stage, commit, and push from the browser.
- **Issues**: a small tracker (design, feature, bug). **Execute** turns an issue into a queued task that stays linked to the issue.
- **Schedules**: cron or interval jobs that create tasks automatically.
- **Settings**: per-project options, such as which MCP servers the sheep can use.

### Task changes and revert

The task detail page shows the prompt, the summary, cost and tokens, the full output, and **every file the task changed**, with a diff per file. You can download the changes as a patch or revert the task. The revert uses a 3-way merge, so edits made after the task are kept. Shepherd stores the snapshots in hidden git refs and does not touch your index, HEAD, or commit history.

![Task changes](assets/webui-task-changes.png)

### MAGI: three models, one answer

MAGI asks three proposers the same question in parallel, each with a different persona (MELCHIOR-1: technical precision, BALTHASAR-2: safety, CASPER-3: practicality). A separate judge compares the answers and writes one synthesis. Low agreement triggers an anonymous debate round. Each proposer can use a different backend, and mixing model families is recommended. MAGI gives advice only: proposers can read files and use the browser, but they cannot edit code.

![MAGI live output](assets/webui-magi-stream.png)

### Wiki

Each project has a markdown wiki for architecture notes, gotchas, and runbooks. Agents read the relevant pages before they start and can add or update pages when they finish, so knowledge carries over between tasks.

![Project wiki](assets/webui-wiki.png)

### More

- **Skills**: reusable prompt templates, global or per project. Agents load the full text only when they need it.
- **Notifications**: PWA Web Push and Discord webhooks for finished and failed tasks and for shepherd reports.
- **Sheep names**: when you add a project, an LLM can give its new sheep a name that fits the project. You can rename sheep at any time.
- **Account**: change the login name and password in **Settings → 공통 (Common)**. Changing them signs out all other sessions.

## Providers

Each sheep uses one provider. You can mix providers in one flock and change a sheep's provider at any time.

| Provider | Backend |
|----------|---------|
| `claude` | Claude Code CLI (default) |
| `opencode` | OpenCode CLI |
| `pi` | Pi coding agent CLI |
| `grok` | Grok CLI |
| `embedded` | Built-in agent loop for any OpenAI-compatible endpoint (llama.cpp, vLLM, Ollama, LM Studio, GitHub Copilot) |
| `magi` | Three-model consensus over the backends above |

The CLI providers use their own login, so Shepherd does not store API keys for them. You add embedded endpoints and configure MAGI in **Settings**.

## Getting started

Requirements: **Go 1.25+**, **Node.js 18+**, and at least one provider (for example the Claude Code CLI in `PATH`, or an OpenAI-compatible endpoint).

```bash
git clone https://github.com/agurrrrr/shepherd.git
cd shepherd
make install            # builds the Web UI and the binary, copies it to ~/.local/bin

shepherd auth setup     # create the Web UI login
shepherd serve -d       # start the daemon
```

Open `http://localhost:8585`, sign in, and add your first project from **Projects → Add project**. Shepherd creates a sheep for it, and you can start sending tasks.

Settings are stored in `~/.shepherd/config.yaml`, and the task database is `~/.shepherd/shepherd.db`. You can change almost every option from the **Settings** page. Uploads accept 10 MB per file by default; set a new limit in **Settings → Common → Max Upload Size**. A shell (`bash`) command may run for 10 minutes by default; set a new limit in **Settings → Common → Bash Tool Timeout**. For access from outside your network, put a reverse proxy with HTTPS in front of the daemon, and give that proxy the same body-size limit as the upload setting.

Shepherd also runs as an MCP server (`shepherd mcp`). This gives agents tools for issues, the wiki, task history, skills, and browser automation.

## Run as a service (systemd)

The recommended way to keep the daemon running is a **systemd user unit**. It needs no `sudo`, and it keeps running after logout when lingering is on. The repo ships `deploy/shepherd.service` for this:

```bash
mkdir -p ~/.config/systemd/user
cp deploy/shepherd.service ~/.config/systemd/user/shepherd.service
systemctl --user daemon-reload
systemctl --user enable --now shepherd
loginctl enable-linger $USER   # keep the daemon running after logout (optional)
```

`deploy/shepherd.system.service` is an optional **system** unit. It starts at boot for one named user. Use it only when you want that behavior, and edit `User=` and `PATH` in it first. It needs `sudo`.

Both units run `serve-foreground`, not `serve -d`. Under systemd the process must stay in the foreground so `Restart=` works. `serve -d` forks a detached child, and systemd cannot track that child.

### PATH and the install directory

Set the install directory to `$HOME/.local/bin`, which is where `make install` and `install.sh` install, and put it **first in your PATH**:

```bash
export PATH="$HOME/.local/bin:$PATH"
```

Put it first so your shell and systemd resolve the binary you just installed, instead of an older copy that lives elsewhere (for example a stale `/usr/local/bin/shepherd`). A systemd unit does **not** read your shell login profile, so the unit file itself must also set `PATH` to include `~/.local/bin`. Without that, the daemon cannot find provider CLIs such as `claude` and `grok` installed under `~/.local/bin`, and tasks fail with `exec: "claude": executable file not found in $PATH`. The shipped units already set this PATH.

If you install provider CLIs with a version manager such as mise, asdf, or nvm, the CLI may live in a shim directory (for example `~/.local/share/mise/shims`) instead of `~/.local/bin`. The PATH baked into the shipped units does not include that directory, so the daemon would still fail to find `claude`. Add your shim directory to `Environment=PATH=` in the unit you installed. To check what PATH the running unit actually uses and what your shell resolves, run:

```bash
systemctl --user cat shepherd              # the unit file in effect (check Environment=PATH=)
systemctl --user show-environment          # environment systemd managers pass to services
command -v claude                          # where YOUR shell resolves the CLI (compare with the PATH above)
```

If `command -v claude` points to a directory that is not in the unit's `Environment=PATH=`, add that directory to the unit and run `systemctl --user daemon-reload && systemctl --user restart shepherd` (restart only when no tasks are in progress).

### Confirm which binary is running

Before you restart the daemon after an install, check that the running process is the binary you just built. An old daemon keeps serving even after a new binary is installed.

```bash
shepherd --version                     # version and build time of the binary on PATH
readlink -f "$(command -v shepherd)"   # absolute path of that binary
# For a systemd-managed daemon, see what the unit actually runs:
systemctl --user cat shepherd 2>/dev/null || systemctl cat shepherd
```

If `shepherd --version` shows an old build time, or the resolved path is not `$HOME/.local/bin/shepherd`, a different copy is running. Restart the daemon only when no tasks are in progress. The daemon is the parent of every agent session, so restarting it ends running tasks.

## Development

```bash
go build ./... && go test ./...
cd web && npm install && npm run dev    # Web UI dev server
```

See [CONTRIBUTING.md](CONTRIBUTING.md) and [ARCHITECTURE.md](ARCHITECTURE.md).

## License

MIT. See [LICENSE](LICENSE).

---

> ***"The Lord is my shepherd; I shall not want."*** (Psalm 23:1)

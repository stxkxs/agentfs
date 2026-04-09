# agentfs

Terminal UI for watching AI agent workspaces in real time — single binary, zero config.

Point it at a directory where your agents persist state, logs, memory, and artifacts. agentfs gives you a live tree view, file previewer with syntax highlighting, activity feed, agent status detection, run history, and search — all updating as files change on disk. Works on any filesystem: local directories, S3 Files mounts, NFS, EFS, FUSE mounts.

Built with Go, [Bubble Tea](https://github.com/charmbracelet/bubbletea), and [Lip Gloss](https://github.com/charmbracelet/lipgloss).

<p align="center">
  <img src="docs/assets/architecture.svg" alt="agentfs architecture" width="100%"/>
</p>

---

## Installation

### go install

```sh
go install github.com/stxkxs/agentfs@latest
```

### Build from source

```sh
git clone https://github.com/stxkxs/agentfs.git
cd agentfs
go build -o agentfs .
./agentfs <directory>
```

---

## Usage

```sh
agentfs <directory>
```

### Example

```sh
# Watch a local agent workspace
agentfs ./workspace

# Watch an S3 Files mount
agentfs /mnt/s3files/agent-workspace
```

### Workspace conventions

agentfs auto-detects agents by scanning top-level subdirectories for:

- **State files** — `state.json`, `status.json`, or `agent.json`
- **Conventional dirs** — `logs/`, `memory/`, `artifacts/`, `tools/`, `output/`

```
workspace/
├── agent-alpha/
│   ├── state.json          # {"status": "running", "task": "research", "step": 3}
│   ├── memory/
│   ├── logs/
│   │   └── run.log
│   └── runs/
│       ├── run-001/
│       └── run-002/
└── agent-beta/
    ├── state.json
    └── artifacts/
```

Status is parsed from the `status` or `state` field in state files and mapped to: `running`, `idle`, `error`, or `done`.

---

## Keybindings

| Key | Action |
|-----|--------|
| `j` / `k` | Navigate up / down |
| `enter` | Open file / expand directory |
| `h` / `l` | Collapse / expand directory |
| `/` | Search in file viewer |
| `n` / `N` | Next / previous search match |
| `g` / `G` | Jump to top / bottom |
| `ctrl+u` / `ctrl+d` | Half-page scroll |
| `tab` / `shift+tab` | Cycle panes |
| `R` | Toggle run history |
| `r` | Reload |
| `esc` | Clear search / exit runs |
| `q` | Quit |

---

## Layout

```
╭──────────────────────────────────────────────────────────────╮
│  agent-alpha ● running (research step:3)  │  agent-beta ○ idle│
╰──────────────────────────────────────────────────────────────╯
╭─── Files ────────╮ ╭─── Preview ──────────────────────────────╮
│  ▼ agent-alpha/  │ │  1  {                                    │
│    state.json    │ │  2    "status": "running",               │
│    ▶ logs/       │ │  3    "task": "research",                │
│    ▶ memory/     │ │  4    "step": 3                          │
│    ▶ runs/       │ │  5  }                                    │
│  ▶ agent-beta/   │ ├─── Activity (5) ─────────────────────────┤
│                  │ │  13:04:05 ~~~ agent-alpha/state.json     │
│                  │ │  13:04:01 +++ agent-beta/artifacts/out   │
╰──────────────────╯ ╰─────────────────────────────────────────╯
```

- **Agent status bar** — auto-detected from state files, color-coded
- **Files** — navigable directory tree, modified files highlighted
- **Preview** — JSON syntax highlighting, log-level coloring, search
- **Activity** — real-time feed of file creates, modifications, deletes

---

## License

[MIT](LICENSE)

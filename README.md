# Skating

A lightweight CI/CD automation tool — Jenkins-like simplicity without the overhead. No database required, single binary deployment.

## Features

- **Zero-dependency deploy** — Single binary, no database, all data in local YAML files
- **Three-layer architecture** — Pipeline DSL + Shell/Lua executor + Yaegi plugin system
- **Docker sandbox** — Isolated builds inside containers for consistent environments
- **Cross-platform** — Windows / Linux / macOS

## Quick Start

### Installation

```bash
# Build from source (requires Go 1.21+)
git clone https://github.com/hpgood/skating.git
cd skating
go build -o skating ./cmd/skating/

# Or use the install scripts
# Linux/macOS:
bash scripts/install.sh

# Windows (PowerShell):
.\scripts\install.ps1
```

### Initialize a Project

```bash
cd /path/to/your/project
skating init          # Generates .skating.yaml template
skating run myproject # Run the build
```

## Commands

| Command | Description |
|---------|-------------|
| `skating init` | Initialize a project with `.skating.yaml` and `scripts/` |
| `skating run <project>` | Execute the pipeline build |
| `skating config <project>` | View project configuration |
| `skating logs <project>` | View build logs (`--last N`, `--id N`) |
| `skating clean <project>` | Clear build logs (keeps BuildID and registry) |
| `skating skill` | Output SKILL guide for AI agents |
| `skating ls` | List all registered projects and their status |

## Three-Layer Architecture

```
┌─────────────────────────────┐
│  Pipeline DSL (YAML)        │  ← Stage / Parallel / When orchestration
├─────────────────────────────┤
│  Executor (Shell + Lua)     │  ← Command execution with Lua sandbox
├─────────────────────────────┤
│  Plugins (Yaegi)            │  ← Go plugins: notifications, reviewers, etc.
└─────────────────────────────┘
```

### Pipeline Configuration

```yaml
# .skating.yaml
name: myproject
image: golang:1.21

pipeline:
  stages:
    - name: build
      steps:
        - name: compile
          type: shell
          script: go build ./...

    - name: test
      parallel: true            # Run steps concurrently
      steps:
        - name: unit-test
          type: shell
          script: go test ./...
        - name: lint
          type: shell
          script: golangci-lint run

    - name: deploy
      steps:
        - name: release
          type: lua             # Lua script execution
          script: |
            log("Build: " .. os.getenv("SKA_BUILD_ID"))
            sh("docker push myapp:" .. os.getenv("SKA_BUILD_ID"))
        - name: notify
          type: shell
          when: branch == "main"  # Conditional execution
          script: |
            echo "Deployed to production"
```

## Environment Variables

Every build automatically injects `SKA_BUILD_ID` (auto-incrementing from 1):

```bash
echo "Building version ${SKA_BUILD_ID}"
```

## Data Storage

All data is stored under `~/.skating/`:

```
~/.skating/
├── projects.yaml              # Project registry
└── logs/<project>/<ID>.log    # Build logs
```

## Examples

See the [examples/](./examples/) directory:

| Example | Description |
|---------|-------------|
| [go-project](./examples/go-project/) | Go CI: build → test → lint |
| [node-project](./examples/node-project/) | Node.js CI: install → build → test |
| [mixed-shell-lua](./examples/mixed-shell-lua/) | Shell + Lua hybrid pipeline |
| [parallel-stages](./examples/parallel-stages/) | Parallel stage execution |
| [conditional-build](./examples/conditional-build/) | When condition examples |

## License

MIT
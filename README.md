# Skating

[中文说明](README.zh-CN.md)

A lightweight CI/CD automation tool — Jenkins-like power, single binary simplicity. No database, no daemons, no config drift. Built for vibe coding.

## Features

- **Zero-dependency deploy** — Single binary, no database, all data in local YAML files
- **Three-layer architecture** — Pipeline DSL + Shell/Lua/SSH/SCP/Sync executor + Yaegi plugin system
- **Docker sandbox** — Isolated builds inside containers for consistent environments
- **Remote deploy** — Native SSH executor plus SFTP-based scp/sync file transfer
- **Cross-platform** — Windows / Linux / macOS

## vs Drone

| | Skating | Drone |
|---|---------|-------|
| **部署** | 单二进制，`go install` 即用 | 需要 Server + Runner + 数据库 |
| **数据库** | 无，本地 YAML 文件 | PostgreSQL / MySQL / SQLite |
| **配置** | 单文件 `.skating.yaml`，在项目内 | `.drone.yml` + Web UI 配置 |
| **执行层** | Shell + Lua (Gopher-Lua 沙箱) | 纯 Shell / 插件 |
| **插件** | Yaegi Go 解释器，原生性能 | Docker 容器化插件 |
| **并行** | Stage 级别原生 goroutine | 多 Runner / 多 Pipeline |
| **缓存** | 无外部依赖 | 需 Redis 或共享卷 |
| **安装成本** | < 30 秒 | 需 Docker Compose / K8s 部署 |

## Quick Start

### Installation

```bash
# Install via go install (requires Go 1.25+)
go install github.com/hpgood/skating/cmd/skating@latest

# Or build from source
git clone https://github.com/hpgood/skating.git
cd skating
mkdir build
go build -ldflags="-X main.version=0.2.0" -o build/skating ./cmd/skating/

# Or build with skating itself (reads the .skating.yaml pipeline in this repo)
skating run

# Verify
./build/skating version

```
```
skating 0.2.0
  commit : abc123
  built  : 2024-01-01
```
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
skating run           # Run the build (project name is optional — defaults to current directory)
```

## Commands

| Command | Description |
|---------|-------------|
| `skating init` | Initialize a project with `.skating.yaml` and `scripts/` |
| `skating run <project>` | Execute the pipeline build (project name optional — defaults to current directory) |
| `skating groups [project]` | List the groups in `pipeline.groups` with their stages and descriptions (name optional — defaults to current directory) |
| `skating stages [project]` | List the stages in declaration order with their descriptions (name optional — defaults to current directory) |
| `skating update` | Sync name/image from the current `.skating.yaml` to the project registry |
| `skating config [project]` | View project configuration (name optional — defaults to current directory) |
| `skating logs [project]` | View build logs (`--last N`, `--id N`; name optional — defaults to current directory) |
| `skating clean [project]` | Clear build logs (keeps BuildID and registry; name optional — defaults to current directory) |
| `skating stats` | Show running tasks and live progress; `--once`, `--all`, `--prune` |
| `skating stop [project] <id>` | Stop a running build, or delete a stale (zombie) record |
| `skating version` | Print version (commit, build date) |
| `skating skill` | Output SKILL guide for AI agents |
| `skating ls` | List all registered projects and their status |

All commands support `--lang zh-CN` for Chinese output.

### `skating run` options

The project name is optional: `skating run` with no argument runs the `.skating.yaml` in the current directory (ad-hoc mode) and auto-registers the project. The positional argument also accepts `<project>+<group>` (or `+<group>` for the current directory) to select a group — equivalent to `-g`/`--group`. The command also supports flags for selective / parameterized builds:

```bash
skating run [project-name[+group]] [flags]
```

| Flag | Format | Description |
|---|---|---|
| `--step`, `-s`  | `<stage>` or `<stage>/<step>` | Run only the named stage (or single step). Other stages / same-stage siblings are reported as `skipped` in the summary but never execute. |
| `--group`, `-g` | `<name>` | Run only the stages in the named group (defined under `pipeline.groups` in `.skating.yaml`). Mutually exclusive with `--step`. |
| `--env`, `-e`   | `KEY=VAL` (repeatable) | Inject a custom environment variable into every step of this run. User values have the **highest priority**: they override same-named built-in `SKA_*`, stage `env`, and step `env` values. |
| `--timeout` | `10m` / `600s` / `600` | Override the run timeout for this invocation (Go duration, or plain seconds). Takes precedence over the `timeout` field in `.skating.yaml`. |

#### Run timeout (`--timeout`)

The whole pipeline has a run timeout; it defaults to **10m** and can be set in
`.skating.yaml`:

```yaml
name: myproj
timeout: 10m        # Go duration ("10m"/"600s") or plain seconds ("600")
pipeline:
  # ...
```

`--timeout` overrides that value for a single run (accepts the same formats):

```bash
skating run myproj --timeout 5m
skating run myproj --timeout 600
```

When the timeout is exceeded, running subprocesses are terminated and the build is
reported as `timeout`.

#### Selective execution with `--step`

```bash
# Run only the 'build' stage (sequential steps inside run end-to-end, even on failure)
skating run myproj --step build

# Run only one step inside a stage; other stages & sibling steps are skipped
skating run myproj --step build/compile

# -s is the short form of --step (same as above)
skating run myproj -s build/compile

# Unknown stage → clear error listing known stages
$ skating run myproj --step nonexistent
ERROR: stage "nonexistent" in --step "nonexistent" not found. Known stages: [build test deploy]
```

Behavior notes:
- Stages not selected are reported with `○ skipped` in the build summary — you always see what was *not* run.
- Within a filtered stage (`--step build`), sequential steps continue past failures so you get a full picture of the stage.
- Step-level filters (`--step build/compile`) preserve the original "stop on first failure" semantic.
- Multi-stage failure ordering: if the selected stage fails, subsequent stages are still marked `skipped` (not silently dropped) so the summary is complete.

#### Selective execution with `--group`

Define named stage sets under `pipeline.groups` and run one with `--group <name>`
(short form `-g`), or the positional shorthand `<project>+<group>` / `+<group>`:

```yaml
name: myproj
pipeline:
  groups:
    all:
      stages: [init, build, test]
      description: "Full pipeline: init, build, test"
    only_build: [build]     # list shorthand; description is empty
    test:       [test]
  stages:
    - name: init
      # ...
    - name: build
      # ...
    - name: test
      # ...
```

Both forms may be mixed under the same `groups:`. A plain list is equivalent to
`{stages: [...]}` with an empty description; the mapping form requires `stages`
and accepts an optional single-line `description` that is display-only (it does
not affect execution).

```bash
# Run every stage in the 'all' group
skating run myproj --group all

# Run only the 'build' stage; the others are reported as skipped
skating run myproj --group only_build

# -g is the short form of --group (same as above)
skating run myproj -g only_build

# Positional shorthand: <project>+<group> is equivalent to --group
skating run myproj+only_build

# ad-hoc: run the current directory project's group (no project name before '+')
skating run +only_build

# Unknown group → clear error listing known groups
$ skating run myproj --group nope
ERROR: group "nope" not found. Known groups: [all only_build test]
```

List the groups and their descriptions (project name optional — defaults to the
current directory, which does not need to be registered):

```bash
$ skating groups myproj
GROUP       STAGES            DESCRIPTION
all         init, build, test Full pipeline: init, build, test
only_build  build             -
test        test              -
```

Behavior notes:
- A group is a *set* of stage names: execution order always follows the declaration
  order in `pipeline.stages`, not the order written inside the group.
- On the first failing stage the group stops; every remaining (selected) stage is
  recorded as `skipped`, and the build as a whole still fails.
- Group entries may only be stage names — for a single step use `--step <stage>/<step>`.
- Group names and stage names live in different namespaces, so `groups.test` may
  reference the stage named `test`.
- `description` is intended to be a single line; it is ignored by `run` and only
  shown by `skating groups` (an empty description is displayed as `-`).
- The positional `+<group>` form is mutually exclusive with `-g`/`--group` (specifying
  both is an error) and with `--step`. `--env` combines with either.
- `+` (not `@`) is the separator because a leading `@` is swallowed by PowerShell
  (it is parsed as a splat of an undefined variable), which would silently run the
  full pipeline instead of one group. `+group` needs no quoting in bash/zsh/cmd/PowerShell.
- A project name containing `+` is not supported (it would be split as a group separator).
- Every group is validated when the config is loaded: an unknown stage reference is
  a hard error even if that group is not the one being run.

#### Listing stages

List the stages declared under `pipeline.stages` together with their descriptions.
Unlike `skating groups`, the output follows the declaration order, which is also the
execution order:

```bash
$ skating stages myproj
STAGE   DESCRIPTION
init    -
build   Compile all platform binaries
test    Run the test suite
```

The project name is optional — without it the `.skating.yaml` in the current
directory is used, and the project does not need to be registered. `description` is
display-only (`run` ignores it) and an empty description is shown as `-`.

#### Custom env vars with `--env`

```bash
# Inject a single var
skating run myproj --env DEPLOY_ENV=staging

# -e is the short form of --env (same as above)
skating run myproj -e DEPLOY_ENV=staging

# Multiple values; later wins on duplicates
skating run myproj --env DEPLOY_ENV=staging --env REGION=us-west-2

# Override a built-in SKA_BUILD_* var (e.g. for replay/debug)
skating run myproj --env SKA_BUILD_ID=999

# Both flags together: deploy only the 'release' step with a custom target
skating run myproj --step deploy/release --env DEPLOY_TARGET=canary
```

`--env` has the highest environment priority: it overrides same-named built-in
`SKA_*` variables as well as stage and step `env` values, and is also visible to
`when` conditions and `env` value interpolation.

Malformed `--env` values (missing `=`, empty KEY) produce a clear error before any step runs.

#### `skating update`

Syncs the current directory's `.skating.yaml` (name / image) into the global project registry:

```bash
cd /path/to/your/project
skating update
```

Behavior:
- **Not registered** → registers the project (name defaults to the directory name if not set in YAML)
- **Same name exists** → updates its `image` and `path`
- **Same path, renamed in YAML** → renames the project record

#### `skating stats`

Shows in-progress tasks with live progress (refreshes every 2s; add `--once` to
render one snapshot and exit). The `Concurrency: N/M` headline counts only tasks
that are actually holding a slot.

```bash
skating stats --once
skating stats --all      # also list recent finished records
skating stats --prune    # delete stale (zombie) records
```

A record becomes **stale** when its heartbeat stops for more than 15s — which
happens when a `skating run` process dies without finishing (e.g. `kill -9`, OOM
killer, power loss, killed container). Stale records:

- do **not** consume a concurrency slot, so they never block new builds;
- keep showing in the list until pruned, and are auto-removed after 7 days.

Use `--prune` to delete them immediately. `--prune` never touches live running or
finished records.

A build that is killed before it finishes has **no log file** and its status in
`projects.yaml` is not updated — logs are written only when the run completes.

#### `skating stop [project] <id>`

Stop a running build on this host, or clean a stale (zombie) record. The project
name is optional and defaults to the current directory.

```bash
skating stats --once          # find the PROJECT and BUILD# first
skating stop 102              # project from the current directory
skating stop training 102     # explicit project
```

Behavior:
- **running**, heartbeat fresh → terminate the process (`SIGTERM` then `SIGKILL` on
  Unix; `taskkill /T /F` on Windows) and mark the record `stopped`;
- **stale** / process already gone → delete the zombie record;
- **already finished** (success/failure/timeout/stopped) → nothing to do.

## Three-Layer Architecture

```
┌─────────────────────────────┐
│  Pipeline DSL (YAML)        │  ← Stage / Parallel / When orchestration
├─────────────────────────────┤
│  Executor                   │  ← Shell + Lua + SSH/SCP/Sync
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
            log("Build: " .. getenv("SKA_BUILD_ID"))
            sh("docker push myapp:" .. getenv("SKA_BUILD_ID"))
        - name: notify
          type: shell
          when: branch == "main"  # Conditional execution
          script: |
            echo "Deployed to production"
```

## Environment Variables

Every build automatically injects these environment variables (UTC, derived from build start time):

| Variable | Type | Example | Description |
|---|---|---|---|
| `SKA_BUILD_ID`        | integer (string) | `1`           | Auto-incrementing build number (from 1) |
| `SKA_BUILD_TIMESTAMP` | integer (string) | `1787139682`  | Build start time as **Unix seconds (UTC)** — easy to sort / diff / arithmetic |
| `SKA_BUILD_DATE`      | string           | `2026-08-19T11:41:22Z` | Build start time as **RFC3339 (UTC)** — human-readable, for reports/logs |
| `SKA_GIT_COMMIT_ID`   | string           | `9a2c1f0d...` | Current git commit id (**full hash**) of the project; empty if the project has no `.git` |
| `SKA_OS`              | string           | `linux`       | Runtime operating system (`runtime.GOOS`): `linux`, `windows`, `darwin`, ... |

Shell scripts:
```bash
echo "Building version ${SKA_BUILD_ID} at ${SKA_BUILD_DATE}"
echo "Local time: $(date -d @${SKA_BUILD_TIMESTAMP})"
```

Lua scripts (sandbox has no `os` module — use `getenv`):
```lua
log("Build: " .. getenv("SKA_BUILD_ID"))
sh("docker push myapp:" .. getenv("SKA_BUILD_ID"))
local ts = getenv("SKA_BUILD_TIMESTAMP")
log("Unix seconds: " .. ts)
```

### Step Environment (`env`)

Each step can define its own environment variables, grouped per platform. The `default` group applies everywhere; other groups (`windows`, `linux`, `darwin`, ...) are injected only when the key matches the current OS, and platform-specific values **override** same-named `default` values:

```yaml
steps:
  - name: compile
    type: shell
    env:
      default:
        version: "0.2.1"
      windows:
        GOARCH: "amd64"
        GOOS: "windows"
        Ext: ".exe"
      linux:
        GOARCH: "amd64"
        GOOS: "linux"
        Ext: ""
    script: |
      go build -ldflags="-X main.version=${version}" -o "build/skating${Ext}" ./cmd/skating
```

### Stage Description (`description`)

A stage may carry an optional single-line `description`, displayed by `skating stages`:

```yaml
stages:
  - name: build
    description: "Compile all platform binaries"
```

It is display-only: it never affects execution, group validation, or environment
variable resolution. An empty description is shown as `-`.

### Stage Condition (`when`)

A stage may carry an optional `when` condition, using the same syntax as the
step-level `when`. When it evaluates to false the whole stage is skipped: every
step inside it is reported as `skipped` with the reason `stage condition not met`,
and the build still succeeds.

```yaml
stages:
  - name: deploy
    when: false                 # disable the stage entirely
    steps:
      - name: release
        type: shell
        script: ./deploy.sh

  - name: publish
    when: branch == "main"      # expression on git context / env
    steps:
      - name: push
        type: shell
        script: docker push myapp
```

- Accepted values: omitted or empty (always run), the boolean literals `true` /
  `false` (case-insensitive), or a comparison expression using
  `==`, `!=`, `>`, `<`, `>=`, `<=`.
- The condition sees the build env, stage env, the cross-step variable pool, and
  the git context (`branch`, `commit`, `git_dirty`).
- It is evaluated before step filtering, so `--step <stage>/<step>` cannot bypass
  `when: false`.
- With `--group`, a skipped stage does not count as a failure: later selected
  stages still run.
- An invalid expression is an error: every step of the stage is reported as
  `failed`.

Step-level `when` accepts the same boolean literals, so `when: false` disables a
single step without failing the build.

### Stage Environment (`env`)

A stage can also define `env`, shared by **all steps inside the stage** (same platform grouping rules as step env). Priority on same-name conflicts: build env (`SKA_*`) < stage env < step env < `--env` (highest):

```yaml
stages:
  - name: build
    env:                 # shared by every step in this stage
      default:
        version: "0.2.1"
      windows:
        Ext: ".exe"
      linux:
        Ext: ""
    steps:
      - name: compile
        type: shell
        script: |
          go build -ldflags="-X main.version=${version}" -o "build/skating${Ext}" ./cmd/skating
```

Stage env variables are visible to scripts (`${VAR}`) and to `when` conditions.

### Variable interpolation in `env` values

Values in stage/step `env` may reference other variables with `${VAR}` or `$VAR`
and concatenate them with literal text. References are resolved by skating before
the step runs, against the already-resolved environment (build env such as
`SKA_*`, `--env`, the cross-step variable pool, and stage env), so a value can be
assembled from other variables:

```yaml
env:
  default:
    version: "0.2.${SKA_BUILD_ID}"   # → "0.2.7" for build #7
```

Resolution is iterative, so a value may reference another variable in the same
`env` block (chains are followed). Undefined variables expand to an empty string.
Note the difference from a `script`: `${version}` inside a `script` is expanded by
the shell at run time, while `${...}` inside an `env` value is expanded by skating
before the step runs.

### Custom Shell with `SKA_SHELL`

By default each `shell` step runs with:

| Platform | Default shell | Effective command |
|---|---|---|
| Windows | `powershell` | `powershell -ExecutionPolicy Bypass -File <script>` |
| Linux / macOS | `bash` | `bash -x <script>` |

Set the `SKA_SHELL` env var (in the step's `env`, or via `--env` on the command line) to choose another shell for a step's `script`. The value is split by spaces: the first token is the shell executable, the rest are passed as arguments, and the script path is appended automatically:

```yaml
env:
  default:
    SKA_SHELL: "pwsh -NoLogo"
# effective: pwsh -NoLogo <script>
```

## Remote Execution & File Transfer (SSH / SCP / Sync)

Steps can run on remote servers and move files between local and remote.
All three types reuse the same connection fields (`host` / `port` / `user` /
`auth` / `secret`) and are powered by a native Go SSH implementation
(`golang.org/x/crypto/ssh`) — no local ssh.exe, sshpass or scp binary needed.

Secrets are referenced by name (stored with `skating secret set <name>`,
password or PEM private key); the value never appears in the yaml or logs.

```yaml
steps:
  # 1) Run commands on a remote server
  - name: remote-build
    type: ssh
    host: 10.0.0.1
    port: "${SERVER_PORT}"       # optional; default 22; ${VAR} supported
    user: deploy
    secret: deploy-pass          # password (default) or "key" auth
    workdir: /opt/app
    script: |
      ./deploy.sh ${SKA_GIT_BRANCH}

  # 2) scp: copy a file/dir local -> remote (push, the default) or back (pull)
  - name: upload-artifact
    type: scp
    host: 10.0.0.1
    user: deploy
    secret: deploy-pass
    local: dist/app-${SKA_GIT_BRANCH}.tar.gz
    remote: /opt/app/app.tar.gz

  # 3) sync: rsync-like incremental mirror of a local directory
  - name: sync-dist
    type: sync
    host: 10.0.0.1
    user: deploy
    secret: deploy-pass
    local: dist
    remote: /opt/app/dist
    delete: true                 # default: remove remote files missing locally
```

Behavior notes:
- `scp` supports both directions: `direction: push` (local -> remote, default)
  and `direction: pull` (remote -> local). Files and directories are copied
  recursively; mtimes are preserved so a follow-up `sync` can skip unchanged
  files.
- `sync` uploads only changed files (compared by size + mtime, second
  precision), and by default mirrors the local directory by deleting remote
  files that no longer exist locally. On first deploy the remote directory may
  not exist yet.
- `local` / `remote` support `${VAR}` expansion from the step env view
  (`SKA_*` system vars, step env, cross-step variable pool). `host`, `port`,
  `user` and `workdir` (ssh) support `${VAR}` too (e.g. `host: 10.0.0.${SEG}`) —
  undefined variables are kept as-is, so a typo surfaces at dial time instead
  of silently targeting a wrong host. `port` may be written as a number or a
  string (`port: 22` or `port: "${SERVER_PORT}"`); an invalid value is reported
  before connecting.
- Build cancellation / timeout closes the SSH connection automatically.

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
| [env-vars](./examples/env-vars/) | Environment variable sources, scopes, and precedence |

### Try `--step` and `--env` with the examples

Each example's `.skating.yaml` uses distinct stage/step names — pick one and experiment:

```bash
# Mixed shell + Lua: 3 stages, perfect for showing --step granularity
skating run mixed-shell-lua-example --step shell-stage             # only stage 1
skating run mixed-shell-lua-example --step lua-stage/lua-build-with-logic  # only one Lua step
skating run mixed-shell-lua-example --step shell-verify            # only verify stage

# Override a SKA_BUILD_* env var to replay a build with a fixed ID
skating run go-project --env SKA_BUILD_ID=999

# Combine: deploy only the release step with a custom target
skating run node-project --step deploy/release --env DEPLOY_TARGET=canary
```

Short flags: `-s` = `--step`, `-g` = `--group`, `-e` = `--env` (and the positional
`<project>+<group>` / `+<group>` shorthand) work the same way:

```bash
skating run mixed-shell-lua-example -s shell-stage
skating run go-project -e SKA_BUILD_ID=999
skating run myproj -g only_build
skating run myproj+only_build
```

See [`examples/mixed-shell-lua/README.md`](./examples/mixed-shell-lua/README.md) for the full worked walkthrough.

## License

MIT
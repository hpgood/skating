# mixed-shell-lua-example

演示在同一个 pipeline 中**混合 Shell 和 Lua 脚本**：

- `shell-stage` —— 用 Shell 打印环境信息
- `lua-stage` —— 用 Lua 调用 `sh()` API 编译 Go 程序
- `shell-verify` —— 用 Shell 校验构建产物

特别地，本 example 是演示 **`skating run --step` 和 `--env`** 标志的最
佳载体：3 个 stage 命名清晰、step 名字具有语义、可选择性跑子集。

---

## 快速开始

```bash
cd examples/mixed-shell-lua
go mod init example.com/mixed-shell-lua 2>/dev/null  # 第一次需要
go mod tidy                                              # 第一次需要

skating init                                             # 注册项目到 ~/.skating
skating run mixed-shell-lua-example                     # 全跑（前提：装好 Go）
```

或者 docker 模式（无需本地 Go）：

```bash
# .skating.yaml 里 image: golang:1.21 已配置；只要 host 有 docker CLI
skating run mixed-shell-lua-example
```

> **没有 Go 也没有 docker？想快速预览？** 临时禁用 docker 检测，所有 step 退回到 host shell：
>
> ```bash
> export SKATING_DISABLE_DOCKER=1
> skating run mixed-shell-lua-example --step shell-stage   # 只跑信息打印，不依赖 go 二进制
> ```
>
> 会有一行 `Warning: Docker image "golang:1.21" configured but docker CLI not found, falling back to host shell.` —— 这是预期行为，不是 bug。

---

## Pipeline 形状

```yaml
stages:
  - name: shell-stage             # 阶段 1: shell
    steps:
      - name: info
  - name: lua-stage               # 阶段 2: 两个 Lua step (sequential)
    steps:
      - name: lua-build-with-logic
      - name: lua-test
  - name: shell-verify            # 阶段 3: shell 校验
    steps:
      - name: verify-artifacts
```

---

## `--step` 用法示例

### 1. 只跑 stage 1 (shell 信息打印)

```bash
skating run mixed-shell-lua-example --step shell-stage
```

跑完 1 个 step；`lua-stage` / `shell-verify` 在 summary 里以 `○ skipped` 显示。

### 2. 只跑 Lua 编译 step（跳过其他所有 step）

```bash
skating run mixed-shell-lua-example --step lua-stage/lua-build-with-logic
```

`lua-test` 和其他两个 stage 都跳过。

### 3. 只跑验证步骤（快校验产物）

```bash
skating run mixed-shell-lua-example --step shell-verify
```

### 4. 跑整个 lua-stage

```bash
skating run mixed-shell-lua-example --step lua-stage
```

sequential step 失败仍继续跑后续 step（让你看到 `lua-build-with-logic`
和 `lua-test` 两个 step 的完整结果）。

### 5. 未知 stage → 友好报错

```bash
$ skating run mixed-shell-lua-example --step deploy
ERROR: --step "deploy" 中 stage "deploy" 不存在。已知 stage: [shell-stage lua-stage shell-verify]
```

---

## `--env` 用法示例

### 1. 注入部署环境

```bash
skating run mixed-shell-lua-example --env DEPLOY_ENV=staging
```

step 内可用 `echo "DEPLOY_ENV=${DEPLOY_ENV}"` (shell) 或
`getenv("DEPLOY_ENV")` (Lua) 读取。

### 2. 多个变量 + 同名覆盖

```bash
skating run mixed-shell-lua-example \
  --env DEPLOY_ENV=staging \
  --env REGION=us-west-2 \
  --env DEPLOY_ENV=production   # 后传入胜出 → DEPLOY_ENV=production
```

### 3. 覆盖内置 `SKA_BUILD_*`（用于重放/调试）

```bash
skating run mixed-shell-lua-example --env SKA_BUILD_ID=999
```

step 里读到的 `SKA_BUILD_ID` 会是 `999` 而不是自增出来的真实 build 号。

### 4. `--step` + `--env` 组合：只重跑验证步骤并指定环境

```bash
skating run mixed-shell-lua-example \
  --step shell-verify \
  --env DEPLOY_ENV=staging \
  --env SKIP_LINT=true
```

---

## 完整可运行示例（期望输出片段）

```bash
$ skating run mixed-shell-lua-example --step lua-stage/lua-build-with-logic
=== Build: mixed-shell-lua-example (Build #1) ===
[shell-stage/info] skipped (stage not selected by --step filter)
[lua-stage/lua-build-with-logic] starting...
[lua-stage/lua-build-with-logic] success (...)
[lua-stage/lua-test] skipped (step not selected by --step filter)
[shell-verify/verify-artifacts] skipped (stage not selected by --step filter)

=== Build SUCCESS ===
--- Build Summary ---
  [shell-stage/info] ○ skipped ()
  [lua-stage/lua-build-with-logic] ✓ success (...)
  [lua-stage/lua-test] ○ skipped ()
  [shell-verify/verify-artifacts] ○ skipped ()
```

注意所有未跑的 step 在 summary 里都看得到 —— 没有任何信息被悄悄漏掉。

---

## 关联文件

- `.skating.yaml` —— pipeline 定义
- `scripts/build.lua` —— Lua 编译脚本（用 `sh()` API 调用 shell）
- `scripts/test.lua` —— Lua 测试脚本
- `main.go` —— Go 程序（`go build -o myapp` 产物）
- `go.mod` —— Go module 声明
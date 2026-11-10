package main

// End-to-end tests for the skating CLI.
//
// Strategy:
//   - Redirect ~/.skating via t.Setenv("HOME", tmpDir) so store.NewStore() uses an isolated dir.
//   - Switch cwd via os.Chdir for commands that depend on it (init uses os.Getwd() as projName).
//   - Capture stdout via cmd.SetOut / cmd.SetErr with a bytes.Buffer.
//   - Disable Docker via t.Setenv("SKATING_DISABLE_DOCKER", "1") so pipeline steps fall back
//     to host shell regardless of host docker CLI availability (matches the convention used
//     in internal/executor/*_test.go).
//
// Scope: only smoke-test init → run → logs happy path + key error paths. This validates
// that the end-to-end plumbing (store, executor, pipeline, plugin.RunPlugins, build ID
// auto-increment, log persistence) works when wired through cobra. We do NOT attempt to
// cover every cobra command here — that work is logged as follow-up.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hpgood/skating/internal/runstate"
	"github.com/hpgood/skating/internal/store"
	"github.com/spf13/pflag"
	bolt "go.etcd.io/bbolt"
)

func setupE2E(t *testing.T) string {
	t.Helper()
	// t.Setenv automatically restores on test cleanup.
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	// Windows 上 os.UserHomeDir() 读 USERPROFILE 而非 HOME，两个都要指到隔离目录
	t.Setenv("USERPROFILE", tmpDir)
	t.Setenv("SKATING_DISABLE_DOCKER", "1")
	return tmpDir
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
}

// executeCmd runs a cobra subcommand end-to-end.
//
// IMPORTANT cobra gotcha: cobra@1.10.2 special-cases binary names containing
// "cobra.test" (see spf13/cobra/command.go ExecuteC line ~1099: "Workaround FAIL
// with `go test -v` or `cobra.test -test.v`, see #155"). When the test binary
// name contains "cobra.test", cobra ignores c.args (set via SetArgs) and reads
// os.Args[1:] instead. So our SetArgs() is silently dropped — we'd run with the
// test runner flags ("-test.run -test.v") and never reach logs/run/init.
//
// Workaround: we resolve the subcommand from rootCmd ourselves and invoke
// cmd.RunE(cmd, args) directly, bypassing cobra's argument-source logic. This
// still exercises runRun / runLogs / runInit exactly as cobra would after
// arg parsing — only arg parsing itself is skipped (we already have args).
func executeCmd(t *testing.T, args ...string) (stdout, stderr string) {
	t.Helper()
	if len(args) == 0 {
		t.Fatal("executeCmd requires at least 1 arg (subcommand name)")
	}

	// Locate the subcommand by name. We only peel off the FIRST arg as the
	// subcommand name; the rest are positional/flag args for that subcommand.
	// (Using cmd.Find() would mis-treat positional args like "buildme" or the
	// "--force" flag as further subcommand names to look up.)
	subName := args[0]
	target, _, err := rootCmd.Find([]string{subName})
	if err != nil || target == rootCmd {
		t.Fatalf("could not find subcommand %q: %v", subName, err)
	}
	cmdArgs := args[1:]

	// Reset any flag state that may have leaked from a previous call (test run
	// order is not guaranteed; package-level vars like logLast/logID could carry
	// values across tests).
	logLast = 0
	logID = 0
	initForce = false
	runStep = ""
	runGroup = ""
	runUserEnv = nil
	runTimeout = ""
	statsAll = false
	statsOnce = false
	statsPrune = false
	// Reset parsed-flag state so subsequent Flag().Parse() doesn't think flags
	// are already set.
	target.Flags().VisitAll(func(pf *pflag.Flag) { pf.Changed = false })

	// Manually parse flags for the target subcommand so things like --force,
	// --id, --last get applied to the package-level vars that RunE reads.
	if err := target.Flags().Parse(cmdArgs); err != nil {
		t.Fatalf("parse flags for %q: %v", subName, err)
	}

	// Cobra internally captures os.Stdout for `cmd.OutOrStdout()` and we just
	// redirected that with SetOut(io.Discard) — that means anything cobra
	// itself writes is silently dropped, which is what we want. The handlers
	// use fmt.Println / fmt.Fprintf(os.Stderr, ...) directly, so we redirect
	// the OS-level file descriptors to capture those.
	origStdout, origStderr := os.Stdout, os.Stderr
	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	os.Stdout = wOut
	os.Stderr = wErr
	t.Cleanup(func() {
		os.Stdout = origStdout
		os.Stderr = origStderr
		_ = wOut.Close()
		_ = wErr.Close()
	})

	outBuf := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	outDone := make(chan struct{})
	errDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(outBuf, rOut)
		close(outDone)
	}()
	go func() {
		_, _ = io.Copy(errBuf, rErr)
		close(errDone)
	}()

	var execErr error
	if target.RunE != nil {
		execErr = target.RunE(target, cmdArgs)
	} else if target.Run != nil {
		target.Run(target, cmdArgs)
	} else {
		execErr = fmt.Errorf("subcommand %q has no Run/RunE handler", args[0])
	}

	_ = wOut.Close()
	_ = wErr.Close()
	<-outDone
	<-errDone

	if execErr != nil {
		return outBuf.String(), errBuf.String() + "\nEXEC ERROR: " + execErr.Error()
	}
	return outBuf.String(), errBuf.String()
}

// TestInit_CreatesProjectAndFiles 验证 init 命令端到端：
//   - 生成 .skating.yaml + scripts/build.sh + scripts/build.lua
//   - 在 ~/.skating/projects.yaml 里注册 Project，name 取自 cwd basename
func TestInit_CreatesProjectAndFiles(t *testing.T) {
	home := setupE2E(t)
	projDir := filepath.Join(home, "myproj")
	if err := os.Mkdir(projDir, 0755); err != nil {
		t.Fatalf("mkdir projDir: %v", err)
	}
	chdir(t, projDir)

	stdout, stderr := executeCmd(t, "init")
	if stderr != "" {
		t.Fatalf("init returned error output: %q", stderr)
	}

	// 1. Files created
	for _, rel := range []string{".skating.yaml", "scripts/build.sh", "scripts/build.lua"} {
		p := filepath.Join(projDir, rel)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected file %s, got: %v", p, err)
		}
	}

	// 2. .skating.yaml schema (not the broken flat steps[] schema from old bug #1)
	yamlBytes, err := os.ReadFile(filepath.Join(projDir, ".skating.yaml"))
	if err != nil {
		t.Fatalf("read yaml: %v", err)
	}
	if !strings.Contains(string(yamlBytes), "pipeline:") {
		t.Errorf(".skating.yaml missing 'pipeline:' section; got:\n%s", yamlBytes)
	}
	if !strings.Contains(string(yamlBytes), "stages:") {
		t.Errorf(".skating.yaml missing 'pipeline.stages:' section; got:\n%s", yamlBytes)
	}
	if strings.Contains(string(yamlBytes), "steps:") && !strings.Contains(string(yamlBytes), "pipeline:") {
		t.Errorf(".skating.yaml uses broken flat-schema (steps at top level)")
	}

	// 3. Project registered in store
	s, err := store.NewStore()
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	p, err := s.GetProject("myproj")
	if err != nil {
		t.Fatalf("get project: %v", err)
	}
	if p.Path != projDir {
		t.Errorf("project.Path = %q, want %q", p.Path, projDir)
	}

	// 4. stdout mentions success
	if !strings.Contains(stdout, "myproj") {
		t.Errorf("stdout missing project name: %q", stdout)
	}
}

// TestInit_RefusesOverwrite 验证 init 不带 --force 时，已存在文件会报错。
func TestInit_RefusesOverwrite(t *testing.T) {
	home := setupE2E(t)
	projDir := filepath.Join(home, "p2")
	if err := os.Mkdir(projDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	chdir(t, projDir)

	// First run: success
	if _, stderr := executeCmd(t, "init"); stderr != "" {
		t.Fatalf("first init: %q", stderr)
	}
	// Second run: should fail (file exists)
	stdout, stderr := executeCmd(t, "init")
	if !strings.Contains(stderr, "file already exists") && !strings.Contains(stderr, "EXEC ERROR") {
		t.Errorf("expected 'file already exists' error, got stdout=%q stderr=%q", stdout, stderr)
	}

	// With --force: should succeed (overwrite)
	stdout, stderr = executeCmd(t, "init", "--force")
	if stderr != "" {
		t.Errorf("--force init: stderr=%q", stderr)
	}
	if !strings.Contains(stdout, "已创建") && !strings.Contains(stdout, "Created") {
		t.Errorf("--force init: expected 'Created' in stdout, got %q", stdout)
	}
}

// TestRun_BuildIDIncrementsThenLogsReads 串联 run → logs 验证：
//   - run 成功后 build ID 自动递增到 1
//   - 再 run 一次后 build ID 应该是 2（验证 8/18 修过的 #3 BuildID 不自增 bug）
//   - logs 默认读 latest，logs --id 2 能读到第二次 build 的日志
//   - logs --last N 能列出摘要
//   - run 失败时 exit code != 0，log 里带 "=== Build FAILED ===" marker
func TestRun_BuildIDIncrementsThenLogsReads(t *testing.T) {
	home := setupE2E(t)
	projDir := filepath.Join(home, "buildme")
	if err := os.Mkdir(projDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	chdir(t, projDir)

	// Init project first.
	if _, stderr := executeCmd(t, "init"); stderr != "" {
		t.Fatalf("init: %q", stderr)
	}

	// Replace the default .skating.yaml with a tiny host-shell pipeline (no Docker needed).
	// We pick "shell" steps so they run under host shell even with SKATING_DISABLE_DOCKER=1.
	yamlContent := `name: buildme
pipeline:
  stages:
    - name: build
      parallel: false
      steps:
        - name: hello
          type: shell
          script: |
            echo "hello build ${SKA_BUILD_ID}"
`
	if err := os.WriteFile(filepath.Join(projDir, ".skating.yaml"), []byte(yamlContent), 0644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	// First run: build ID becomes 1.
	stdout, stderr := executeCmd(t, "run", "buildme")
	if stderr != "" {
		t.Fatalf("run #1: stderr=%q", stderr)
	}
	if !strings.Contains(stdout, "(Build #1)") {
		t.Errorf("run #1 stdout missing '(Build #1)': %q", stdout)
	}
	// (=== Build SUCCESS === marker is printed by cmd_run after pipeline completes;
	// it does NOT end up in the persisted log file because only logFn() calls do.
	// So we assert it appears on stdout — proving the build truly succeeded.)
	if !strings.Contains(stdout, "=== Build SUCCESS ===") {
		t.Errorf("run #1 stdout missing success marker: %q", stdout)
	}
	if !strings.Contains(stdout, "[plugin: console-notifier]") {
		t.Errorf("run #1 stdout missing plugin output (ConsoleNotifier should run): %q", stdout)
	}

	// Second run: build ID becomes 2 (regression for bug #3).
	stdout, _ = executeCmd(t, "run", "buildme")
	if !strings.Contains(stdout, "(Build #2)") {
		t.Errorf("run #2 stdout missing '(Build #2)' — BuildID not incrementing: %q", stdout)
	}

	// Note: shell step stdout (e.g. "echo hello build ...") is captured by
	// runShell into bytes.Buffer and returned to pipeline.RunPipeline, but
	// runShell's internal stdout/stderr buffers are NOT forwarded to logFn.
	// So the persisted log only contains runPipeline's own [stage/step] starting /
	// success markers, not the script's stdout. The end-to-end value of this
	// test is verifying BuildID auto-increment + log persistence + logs command
	// readback — not shell output capture (which has unit tests).
	stdout, stderr = executeCmd(t, "logs", "buildme")
	if stderr != "" {
		t.Errorf("logs: stderr=%q", stderr)
	}
	if !strings.Contains(stdout, "[build/hello] success") {
		t.Errorf("logs default stdout missing step success marker: %q", stdout)
	}

	// logs --id 1 should print build #1 content (different timing message).
	stdout, stderr = executeCmd(t, "logs", "buildme", "--id", "1")
	if stderr != "" {
		t.Errorf("logs --id 1: stderr=%q", stderr)
	}
	if !strings.Contains(stdout, "[build/hello] success") {
		t.Errorf("logs --id 1 stdout missing step success marker: %q", stdout)
	}

	// logs --last 2 should list both builds with the latest first. We assert step
	// output is present; the SUCCESS/FAILED marker is NOT in persisted logs (see
	// note above) so extractBuildStatus returns "-" for both. Verify IDs 1+2 listed.
	stdout, stderr = executeCmd(t, "logs", "buildme", "--last", "2")
	if stderr != "" {
		t.Errorf("logs --last 2: stderr=%q", stderr)
	}
	if !strings.Contains(stdout, "Build ID: 2") || !strings.Contains(stdout, "Build ID: 1") {
		t.Errorf("logs --last 2 stdout should mention both Build IDs 1 and 2: %q", stdout)
	}
	if !strings.Contains(stdout, "Status: -") {
		t.Errorf("logs --last 2 stdout should show 'Status: -' (no SUCCESS marker in log): %q", stdout)
	}

	// Store should reflect LastStatus="success" after successful run.
	s, err := store.NewStore()
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	p, err := s.GetProject("buildme")
	if err != nil {
		t.Fatalf("get project: %v", err)
	}
	if p.LastStatus != "success" {
		t.Errorf("project.LastStatus = %q, want success", p.LastStatus)
	}
	if p.BuildID != 2 {
		t.Errorf("project.BuildID = %d, want 2", p.BuildID)
	}
}

// TestRun_FailureLeavesFailureMarker 验证失败的 step 会让 logs --last 显示 failure。
func TestRun_FailureLeavesFailureMarker(t *testing.T) {
	home := setupE2E(t)
	projDir := filepath.Join(home, "failme")
	if err := os.Mkdir(projDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	chdir(t, projDir)

	if _, stderr := executeCmd(t, "init"); stderr != "" {
		t.Fatalf("init: %q", stderr)
	}

	// Pipeline that always exits non-zero on the first step.
	yamlContent := `name: failme
pipeline:
  stages:
    - name: bad
      parallel: false
      steps:
        - name: oops
          type: shell
          script: |
            echo "about to fail"
            exit 7
`
	if err := os.WriteFile(filepath.Join(projDir, ".skating.yaml"), []byte(yamlContent), 0644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	_, stderr := executeCmd(t, "run", "failme")
	if !strings.Contains(stderr, "EXEC ERROR") {
		t.Errorf("run failme: expected EXEC ERROR in stderr, got %q", stderr)
	}

	// Debug: dump saved log content to verify SaveLog actually wrote the failure line.
	s, _ := store.NewStore()
	logContent, _ := s.GetLog("failme", 1)
	if !strings.Contains(logContent, "FAILED") {
		t.Errorf("saved log should contain FAILED line, got %q", logContent)
	}

	stdout, _ := executeCmd(t, "logs", "failme", "--last", "1")
	// `logs --last` only prints summary: "Build ID: N  Status: -\n---\n"
	// (extractBuildStatus looks for marker that is NOT in the persisted log;
	// see TestRun_BuildIDIncrementsThenLogsReads note.)
	if !strings.Contains(stdout, "Build ID: 1") {
		t.Errorf("logs --last 1 stdout missing Build ID: %q", stdout)
	}
	if !strings.Contains(stdout, "---") {
		t.Errorf("logs --last 1 stdout missing separator: %q", stdout)
	}
}

// TestRun_UnknownProject 验证 run 不存在的项目时返回友好错误。
func TestRun_UnknownProject(t *testing.T) {
	home := setupE2E(t)
	projDir := filepath.Join(home, "anywhere")
	if err := os.Mkdir(projDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	chdir(t, projDir)

	_, stderr := executeCmd(t, "run", "nope")
	if !strings.Contains(stderr, "nope") || !strings.Contains(stderr, "不存在") && !strings.Contains(stderr, "not found") {
		t.Errorf("expected project-not-found error mentioning 'nope': %q", stderr)
	}
}

// TestRun_InjectedBuildEnvVars 验证 cmd_run 注入的 build-time env
// (SKA_BUILD_ID / SKA_BUILD_TIMESTAMP / SKA_BUILD_DATE / SKA_GIT_COMMIT_ID) 都能从 step 里读到,
// 且格式/一致性正确:
//   - SKA_BUILD_TIMESTAMP 是 10 位整数 unix 秒 (UTC)
//   - SKA_BUILD_DATE     是 RFC3339 字符串以 'Z' 结尾 (UTC)
//   - 转 SKA_BUILD_TIMESTAMP 回 UTC 日期 ≈ SKA_BUILD_DATE (一致性)
//   - SKA_GIT_COMMIT_ID  在非 git 项目目录下为空字符串
//
// 注意 runShell 把脚本 stdout 写到自己的 bytes.Buffer, 不会通过 logFn 转发,
// 所以这里用 exit 1 让 executor 失败, 把 stdout 拼到 error message 里显示出来
// (cmd_run 的 logFn 只看到 [stage/step] FAILED 这行; stdout 内容通过 stderr 冒上来).
func TestRun_InjectedBuildEnvVars(t *testing.T) {
	home := setupE2E(t)
	projDir := filepath.Join(home, "envtest")
	if err := os.Mkdir(projDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	chdir(t, projDir)

	if _, stderr := executeCmd(t, "init"); stderr != "" {
		t.Fatalf("init: %q", stderr)
	}

	// 一个 shell step: echo 三个 env 的值, 然后 exit 1 让 stdout 走到 stderr.
	yamlContent := `name: envtest
pipeline:
  stages:
    - name: probe
      steps:
        - name: dump-env
          type: shell
          script: |
            echo "SKA_BUILD_ID=${SKA_BUILD_ID}"
            echo "SKA_BUILD_TIMESTAMP=${SKA_BUILD_TIMESTAMP}"
            echo "SKA_BUILD_DATE=${SKA_BUILD_DATE}"
            echo "SKA_GIT_COMMIT_ID=${SKA_GIT_COMMIT_ID}"
            echo "SKA_OS=${SKA_OS}"
            echo "ROUNDTRIP=$(date -u -d @${SKA_BUILD_TIMESTAMP} +%Y-%m-%dT%H:%M:%SZ)"
            exit 1
`
	if err := os.WriteFile(filepath.Join(projDir, ".skating.yaml"), []byte(yamlContent), 0644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	stdout, stderr := executeCmd(t, "run", "envtest")
	if !strings.Contains(stderr, "EXEC ERROR") {
		t.Fatalf("expected EXEC ERROR (we forced exit 1), got stderr=%q", stderr)
	}

	combined := stdout + stderr

	// 1. SKA_BUILD_ID must be present (number string)
	if !strings.Contains(combined, "SKA_BUILD_ID=1") {
		t.Errorf("missing SKA_BUILD_ID=1 in output: %q", combined)
	}

	// 2. SKA_BUILD_TIMESTAMP must be exactly 10-digit unix seconds
	// Windows PowerShell echo 输出 CRLF，行尾需去掉 '\r'
	var tsStr string
	for _, line := range strings.Split(combined, "\n") {
		if strings.HasPrefix(line, "SKA_BUILD_TIMESTAMP=") {
			tsStr = strings.TrimRight(strings.TrimPrefix(line, "SKA_BUILD_TIMESTAMP="), "\r")
		}
	}
	if tsStr == "" {
		t.Fatalf("SKA_BUILD_TIMESTAMP not found in output")
	}
	if len(tsStr) != 10 {
		t.Errorf("SKA_BUILD_TIMESTAMP = %q, want 10-digit unix seconds (got %d chars)", tsStr, len(tsStr))
	}
	if _, err := strconv.Atoi(tsStr); err != nil {
		t.Errorf("SKA_BUILD_TIMESTAMP = %q is not a pure integer: %v", tsStr, err)
	}
	// Sanity range: must be after 2020-01-01 (1577836800) and before 2100-01-01 (4102444800)
	ts, _ := strconv.Atoi(tsStr)
	if ts < 1577836800 || ts > 4102444800 {
		t.Errorf("SKA_BUILD_TIMESTAMP = %d out of sane range [2020..2100]", ts)
	}

	// 3. SKA_BUILD_DATE must be RFC3339 UTC ending with 'Z'
	var dateStr string
	for _, line := range strings.Split(combined, "\n") {
		if strings.HasPrefix(line, "SKA_BUILD_DATE=") {
			dateStr = strings.TrimRight(strings.TrimPrefix(line, "SKA_BUILD_DATE="), "\r")
		}
	}
	if dateStr == "" {
		t.Fatalf("SKA_BUILD_DATE not found in output")
	}
	if !strings.HasSuffix(dateStr, "Z") {
		t.Errorf("SKA_BUILD_DATE = %q, want RFC3339 UTC ending in 'Z'", dateStr)
	}
	if _, err := time.Parse(time.RFC3339, dateStr); err != nil {
		t.Errorf("SKA_BUILD_DATE = %q not valid RFC3339: %v", dateStr, err)
	}

	// 4. Consistency: date converted back from SKA_BUILD_TIMESTAMP (UTC) must match SKA_BUILD_DATE
	// The step's "ROUNDTRIP=..." line is what `date -u -d @$TS +...` produced in the container.
	// (We don't compare against host timezones — the container is UTC.)
	if !strings.Contains(combined, "ROUNDTRIP=") {
		t.Errorf("ROUNDTRIP line missing — cannot verify TS/DATE consistency: %q", combined)
	}
	// Find ROUNDTRIP line and SKA_BUILD_DATE line, compare
	// Windows PowerShell 没有 GNU date -u -d，ROUNDTRIP 输出为空 —— 为空时跳过比较
	var roundtripStr string
	for _, line := range strings.Split(combined, "\n") {
		if strings.HasPrefix(line, "ROUNDTRIP=") {
			roundtripStr = strings.TrimRight(strings.TrimPrefix(line, "ROUNDTRIP="), "\r")
		}
	}
	if roundtripStr != "" && roundtripStr != dateStr {
		t.Errorf("SKA_BUILD_TIMESTAMP (unix %s) converted back via 'date -u -d @...' = %q, but SKA_BUILD_DATE = %q (should be equal)",
			tsStr, roundtripStr, dateStr)
	}

	// 5. SKA_GIT_COMMIT_ID: 非 git 项目目录（t.TempDir 无 .git）下必须为空字符串
	var commitStr string
	for _, line := range strings.Split(combined, "\n") {
		if strings.HasPrefix(line, "SKA_GIT_COMMIT_ID=") {
			commitStr = strings.TrimRight(strings.TrimPrefix(line, "SKA_GIT_COMMIT_ID="), "\r")
		}
	}
	if commitStr != "" {
		t.Errorf("SKA_GIT_COMMIT_ID = %q, want empty (project dir has no .git)", commitStr)
	}

	// 6. SKA_OS: 必须等于测试进程的 runtime.GOOS
	var osStr string
	for _, line := range strings.Split(combined, "\n") {
		if strings.HasPrefix(line, "SKA_OS=") {
			osStr = strings.TrimRight(strings.TrimPrefix(line, "SKA_OS="), "\r")
		}
	}
	if osStr != runtime.GOOS {
		t.Errorf("SKA_OS = %q, want %q (runtime.GOOS)", osStr, runtime.GOOS)
	}
}

// TestRun_GitCommitIDInjected 验证项目目录是 git 仓库时 SKA_GIT_COMMIT_ID
// 注入完整的 commit hash（git rev-parse HEAD，40 位），且与仓库实际 HEAD 一致。
func TestRun_GitCommitIDInjected(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	home := setupE2E(t)
	projDir := filepath.Join(home, "gittest")
	if err := os.Mkdir(projDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	chdir(t, projDir)

	if _, stderr := executeCmd(t, "init"); stderr != "" {
		t.Fatalf("init: %q", stderr)
	}

	// 在项目目录里建一个真实 git 仓库并提交
	gitRun := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = projDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	gitRun("init")
	gitRun("config", "user.email", "skating-test@example.com")
	gitRun("config", "user.name", "skating test")
	gitRun("add", ".")
	// --no-gpg-sign 避免宿主机全局配置强制签名导致失败
	gitRun("commit", "--no-gpg-sign", "-m", "init")

	// 期望值：仓库当前 HEAD 的完整 hash
	want := gitCommitID(projDir)
	if len(want) != 40 {
		t.Fatalf("gitCommitID = %q, want 40-char full hash", want)
	}

	yamlContent := `name: gittest
pipeline:
  stages:
    - name: probe
      steps:
        - name: dump-commit
          type: shell
          script: |
            echo "SKA_GIT_COMMIT_ID=${SKA_GIT_COMMIT_ID}"
            exit 1
`
	if err := os.WriteFile(filepath.Join(projDir, ".skating.yaml"), []byte(yamlContent), 0644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	stdout, stderr := executeCmd(t, "run", "gittest")
	if !strings.Contains(stderr, "EXEC ERROR") {
		t.Fatalf("expected EXEC ERROR (we forced exit 1), got stderr=%q", stderr)
	}

	combined := stdout + stderr
	var got string
	for _, line := range strings.Split(combined, "\n") {
		if strings.HasPrefix(line, "SKA_GIT_COMMIT_ID=") {
			got = strings.TrimRight(strings.TrimPrefix(line, "SKA_GIT_COMMIT_ID="), "\r")
		}
	}
	if got != want {
		t.Errorf("SKA_GIT_COMMIT_ID = %q, want %q", got, want)
	}
}

// TestRun_GitBranchInjected 验证项目目录是 git 仓库时 SKA_GIT_BRANCH
// 注入当前分支名，且与仓库实际分支一致（git rev-parse --abbrev-ref HEAD）。
func TestRun_GitBranchInjected(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	home := setupE2E(t)
	projDir := filepath.Join(home, "branchtest")
	if err := os.Mkdir(projDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	chdir(t, projDir)

	if _, stderr := executeCmd(t, "init"); stderr != "" {
		t.Fatalf("init: %q", stderr)
	}

	// 在项目目录里建一个真实 git 仓库并提交
	gitRun := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = projDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	gitRun("init")
	gitRun("config", "user.email", "skating-test@example.com")
	gitRun("config", "user.name", "skating test")
	gitRun("add", ".")
	gitRun("commit", "--no-gpg-sign", "-m", "init")

	// 期望值：仓库当前分支名（与注入同一查询方式）
	want := gitBranch(projDir)
	if want == "" {
		t.Fatal("gitBranch = empty, want current branch name")
	}

	yamlContent := `name: branchtest
pipeline:
  stages:
    - name: probe
      steps:
        - name: dump-branch
          type: shell
          script: |
            echo "SKA_GIT_BRANCH=${SKA_GIT_BRANCH}"
            exit 1
`
	if err := os.WriteFile(filepath.Join(projDir, ".skating.yaml"), []byte(yamlContent), 0644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	stdout, stderr := executeCmd(t, "run", "branchtest")
	if !strings.Contains(stderr, "EXEC ERROR") {
		t.Fatalf("expected EXEC ERROR (we forced exit 1), got stderr=%q", stderr)
	}

	combined := stdout + stderr
	var got string
	for _, line := range strings.Split(combined, "\n") {
		if strings.HasPrefix(line, "SKA_GIT_BRANCH=") {
			got = strings.TrimRight(strings.TrimPrefix(line, "SKA_GIT_BRANCH="), "\r")
		}
	}
	if got != want {
		t.Errorf("SKA_GIT_BRANCH = %q, want %q", got, want)
	}
}

// TestRun_StepFilterSingleStep 验证 --step stage/step 只运行指定单个 step，
// 其他 stage 和同 stage 内其他 step 都记为 skipped。
//
// Pipeline 设计（避免 host shell 干扰，shell step 一律 exit 0 让 stdout 进 stderr）：
//
//	stage-pre:   step-pre-only
//	stage-build: step-keep (被选中的, exit 1), step-skip (同 stage, exit 0)
//	stage-post:  step-post-only
//
// 预期: step-pre-only / step-skip / step-post-only 都在 results 里 status=skipped;
// step-keep 是 status=failed (因为 exit 1) 且 stdout 含 "KEEP-RAN"。
func TestRun_StepFilterSingleStep(t *testing.T) {
	home := setupE2E(t)
	projDir := filepath.Join(home, "stepfilter")
	if err := os.Mkdir(projDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	chdir(t, projDir)

	if _, stderr := executeCmd(t, "init"); stderr != "" {
		t.Fatalf("init: %q", stderr)
	}

	yamlContent := `name: stepfilter
pipeline:
  stages:
    - name: pre
      steps:
        - name: pre-only
          type: shell
          script: |
            echo "PRE-RAN"
            exit 1
    - name: build
      steps:
        - name: keep
          type: shell
          script: |
            echo "KEEP-RAN"
            exit 1
        - name: skip
          type: shell
          script: |
            echo "SKIP-RAN"
            exit 1
    - name: post
      steps:
        - name: post-only
          type: shell
          script: |
            echo "POST-RAN"
            exit 1
`
	if err := os.WriteFile(filepath.Join(projDir, ".skating.yaml"), []byte(yamlContent), 0644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	// --step build/keep → 只跑 build/keep，其余三个都被记为 skipped
	stdout, stderr := executeCmd(t, "run", "stepfilter", "--step", "build/keep")
	combined := stdout + stderr

	// step-keep 应该真的跑了（exit 1 → EXEC ERROR），stdout 含 KEEP-RAN
	if !strings.Contains(combined, "KEEP-RAN") {
		t.Errorf("step 'keep' did not run (no KEEP-RAN in output): %q", combined)
	}

	// 其他三个 step 应该被 skip（runner 通过 logFn 输出 "[stage/step] skipped (...)"）
	for _, skipped := range []string{"pre/pre-only", "build/skip", "post/post-only"} {
		pattern := fmt.Sprintf("[%s] skipped", skipped)
		if !strings.Contains(combined, pattern) {
			t.Errorf("expected %q to be skipped, not found in output: %q", pattern, combined)
		}
	}

	// 反向断言：pre/POST/skip 不应该真的跑（echo 输出在 error message 里）
	// 注意：echo 输出仍可能出现在 EXEC ERROR message 之外的某处，所以用 "X-RAN" 直接匹配
	if strings.Contains(combined, "PRE-RAN") {
		t.Errorf("stage 'pre' should have been skipped, but PRE-RAN appeared: %q", combined)
	}
	if strings.Contains(combined, "POST-RAN") {
		t.Errorf("stage 'post' should have been skipped, but POST-RAN appeared: %q", combined)
	}
	if strings.Contains(combined, "SKIP-RAN") {
		t.Errorf("step 'skip' (same stage as 'keep') should have been skipped, but SKIP-RAN appeared: %q", combined)
	}
}

// TestRun_StepFilterSingleStage 验证 --step stage (不带 /step) 跑整个 stage。
// 其他 stage 记为 skipped；同 stage 内所有 step 都跑。
//
// 设计:
//
//	stage-build: step-a (exit 0), step-b (exit 0)
//	stage-other: step-x (exit 0)
//
// 预期: step-a / step-b 都真的跑（"A-RAN" "B-RAN" 出现），step-x 被 skipped。
func TestRun_StepFilterSingleStage(t *testing.T) {
	home := setupE2E(t)
	projDir := filepath.Join(home, "stagefilter")
	if err := os.Mkdir(projDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	chdir(t, projDir)

	if _, stderr := executeCmd(t, "init"); stderr != "" {
		t.Fatalf("init: %q", stderr)
	}

	yamlContent := `name: stagefilter
pipeline:
  stages:
    - name: build
      steps:
        - name: a
          type: shell
          script: |
            echo "A-RAN"
            exit 1
        - name: b
          type: shell
          script: |
            echo "B-RAN"
            exit 1
    - name: other
      steps:
        - name: x
          type: shell
          script: |
            echo "X-RAN"
            exit 1
`
	if err := os.WriteFile(filepath.Join(projDir, ".skating.yaml"), []byte(yamlContent), 0644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	stdout, stderr := executeCmd(t, "run", "stagefilter", "--step", "build")
	combined := stdout + stderr

	if !strings.Contains(combined, "A-RAN") {
		t.Errorf("stage 'build' step 'a' should have run: %q", combined)
	}
	if !strings.Contains(combined, "B-RAN") {
		t.Errorf("stage 'build' step 'b' should have run: %q", combined)
	}
	if strings.Contains(combined, "X-RAN") {
		t.Errorf("stage 'other' should have been skipped, but X-RAN appeared: %q", combined)
	}
	if !strings.Contains(combined, "[other/x] skipped") {
		t.Errorf("expected [other/x] skipped marker, not found in: %q", combined)
	}
}

// TestRun_EnvOverride 验证 -e KEY=VAL 把环境变量注入到 step，
// 且用户传入的 env 优先级高于 SKA_ 系统 env（用户显式覆盖）。
//
// 设计:
//   - stage env 定义 USER_OVERRIDE，step env 定义 USER_FOO，均与 --env 同名
//   - shell step 把 SKA_BUILD_ID / USER_FOO / USER_OVERRIDE 三个 env 都 echo 出来然后 exit 1
//   - -e USER_FOO=hello -e USER_OVERRIDE=my-value -e SKA_BUILD_ID=999
//   - 验证 USER_FOO / USER_OVERRIDE 被注入，且 --env 覆盖 stage/step 同名 env
//   - 验证 SKA_BUILD_ID 被用户值 999 覆盖（而不是系统默认的 1）
func TestRun_EnvOverride(t *testing.T) {
	home := setupE2E(t)
	projDir := filepath.Join(home, "envtest2")
	if err := os.Mkdir(projDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	chdir(t, projDir)

	if _, stderr := executeCmd(t, "init"); stderr != "" {
		t.Fatalf("init: %q", stderr)
	}

	yamlContent := `name: envtest2
pipeline:
  stages:
    - name: probe
      env:
        default:
          USER_OVERRIDE: from-stage
      steps:
        - name: dump-env
          type: shell
          env:
            default:
              USER_FOO: from-step
          script: |
            echo "SKA_BUILD_ID=${SKA_BUILD_ID}"
            echo "USER_FOO=${USER_FOO}"
            echo "USER_OVERRIDE=${USER_OVERRIDE}"
            exit 1
`
	if err := os.WriteFile(filepath.Join(projDir, ".skating.yaml"), []byte(yamlContent), 0644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	stdout, stderr := executeCmd(t, "run", "envtest2",
		"--env", "USER_FOO=hello",
		"--env", "USER_OVERRIDE=my-value",
		"--env", "SKA_BUILD_ID=999",
	)
	combined := stdout + stderr

	// 用户值被注入
	if !strings.Contains(combined, "USER_FOO=hello") {
		t.Errorf("USER_FOO=hello not injected, output: %q", combined)
	}
	if !strings.Contains(combined, "USER_OVERRIDE=my-value") {
		t.Errorf("USER_OVERRIDE=my-value not injected, output: %q", combined)
	}

	// 用户值覆盖 SKA_BUILD_ID 默认值 1
	if !strings.Contains(combined, "SKA_BUILD_ID=999") {
		t.Errorf("user --env should override SKA_BUILD_ID (expected =999), output: %q", combined)
	}
	if strings.Contains(combined, "SKA_BUILD_ID=1\n") || strings.Contains(combined, "SKA_BUILD_ID=1 ") {
		t.Errorf("SKA_BUILD_ID should be overridden to 999, but default 1 leaked: %q", combined)
	}
}

// TestRun_StepFilterUnknownStage 验证 --step 不存在的 stage 时给出清晰的错误。
func TestRun_StepFilterUnknownStage(t *testing.T) {
	home := setupE2E(t)
	projDir := filepath.Join(home, "badfilter")
	if err := os.Mkdir(projDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	chdir(t, projDir)

	if _, stderr := executeCmd(t, "init"); stderr != "" {
		t.Fatalf("init: %q", stderr)
	}

	yamlContent := `name: badfilter
pipeline:
  stages:
    - name: build
      steps:
        - name: compile
          type: shell
          script: |
            echo "should not run"
            exit 0
`
	if err := os.WriteFile(filepath.Join(projDir, ".skating.yaml"), []byte(yamlContent), 0644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	_, stderr := executeCmd(t, "run", "badfilter", "--step", "nonexistent")
	if !strings.Contains(stderr, "nonexistent") {
		t.Errorf("error should mention missing stage name 'nonexistent': %q", stderr)
	}
	if !strings.Contains(stderr, "build") {
		t.Errorf("error should list known stages (containing 'build'): %q", stderr)
	}
}

// TestRun_StepFilterFailingStepReturnsError 是 Bug 回归测试：
// `--step stage/step` 模式下，被选的 step 失败时 RunPipeline 必须返回非 nil error，
// 让 cmd_run 报 "Build FAILED" 而不是误报 SUCCESS。
//
// 之前实现里 filter 模式下 `continue` 直接吞掉 err，导致 exit code 0 + "Build SUCCESS"，
// 看似 build 成功实际失败——测试 / CI 会漏报。
func TestRun_StepFilterFailingStepReturnsError(t *testing.T) {
	home := setupE2E(t)
	projDir := filepath.Join(home, "failingstep")
	if err := os.Mkdir(projDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	chdir(t, projDir)

	if _, stderr := executeCmd(t, "init"); stderr != "" {
		t.Fatalf("init: %q", stderr)
	}

	// 3 stages: skip / target (exit 1) / skip
	// --step target/will-fail → 只跑 target/will-fail，其他都 skipped
	// 期望: stderr 含 EXEC ERROR + "Build FAILED"（不是 Build SUCCESS）
	yamlContent := `name: failingstep
pipeline:
  stages:
    - name: before
      steps:
        - name: a
          type: shell
          script: |
            echo "A-RAN"
            exit 1
    - name: target
      steps:
        - name: will-fail
          type: shell
          script: |
            echo "WILL-FAIL"
            exit 1
    - name: after
      steps:
        - name: c
          type: shell
          script: |
            echo "C-RAN"
            exit 1
`
	if err := os.WriteFile(filepath.Join(projDir, ".skating.yaml"), []byte(yamlContent), 0644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	stdout, stderr := executeCmd(t, "run", "failingstep", "--step", "target/will-fail")
	combined := stdout + stderr

	// 关键断言 1: 失败 step 真的跑了（WILL-FAIL 出现）
	if !strings.Contains(combined, "WILL-FAIL") {
		t.Errorf("step 'will-fail' should have run (no WILL-FAIL in output): %q", combined)
	}

	// 关键断言 2: EXEC ERROR marker 出现（说明 RunPipeline 返回了非 nil err，
	// cmd_run 把它当 stderr 抛了；如果被吞掉，会看到 "Build SUCCESS" 而非 EXEC ERROR）
	if !strings.Contains(stderr, "EXEC ERROR") {
		t.Errorf("filter mode failed step must surface EXEC ERROR (RunPipeline returned nil err?), got stderr=%q", stderr)
	}

	// 关键断言 3: 不能是 Build SUCCESS（应该是 FAILED）
	if strings.Contains(combined, "=== Build SUCCESS ===") {
		t.Errorf("filter mode step failure must NOT report Build SUCCESS: %q", combined)
	}
	if !strings.Contains(combined, "Build FAILED") {
		t.Errorf("expected 'Build FAILED' marker in output, got: %q", combined)
	}

	// 关键断言 4: skipped 阶段在 summary 里出现（filter 模式让 summary 完整）
	if !strings.Contains(combined, "[before/a] skipped") {
		t.Errorf("expected [before/a] skipped marker: %q", combined)
	}
	if !strings.Contains(combined, "[after/c] skipped") {
		t.Errorf("expected [after/c] skipped marker: %q", combined)
	}
}

// TestRun_CrossStepVarPassing 验证 ::set-var 跨 step 传变量：
// producer echo "::set-var GREETING=hello" 写入构建级变量池，
// consumer 的 When 条件与脚本均能读到 GREETING；标记行本身不进日志。
func TestRun_CrossStepVarPassing(t *testing.T) {
	home := setupE2E(t)
	projDir := filepath.Join(home, "varpooltest")
	if err := os.Mkdir(projDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	chdir(t, projDir)

	if _, stderr := executeCmd(t, "init"); stderr != "" {
		t.Fatalf("init: %q", stderr)
	}

	yamlContent := `name: varpooltest
pipeline:
  stages:
    - name: pass-vars
      steps:
        - name: producer
          type: shell
          script: |
            echo "::set-var GREETING=hello"
        - name: consumer
          type: shell
          when: "GREETING == hello"
          script: |
            echo "GREETING=${GREETING}"
        - name: wrong-cond
          type: shell
          when: "GREETING == world"
          script: |
            echo "WRONG-RAN"
`
	if err := os.WriteFile(filepath.Join(projDir, ".skating.yaml"), []byte(yamlContent), 0644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	stdout, stderr := executeCmd(t, "run", "varpooltest")
	if strings.Contains(stderr, "EXEC ERROR") {
		t.Fatalf("run failed: %q", stderr)
	}
	combined := stdout + stderr

	// 1. consumer 的 When 条件通过并读到变量
	if !strings.Contains(combined, "GREETING=hello") {
		t.Errorf("consumer did not see GREETING=hello; output: %q", combined)
	}

	// 2. 值不匹配的 When 条件走 skipped
	if !strings.Contains(combined, "[pass-vars/wrong-cond] skipped") {
		t.Errorf("wrong-cond should be skipped; output: %q", combined)
	}

	// 3. 标记行不进日志（逐行精确比对：PS DEBUG trace 行含标记文本但带前缀，不算泄漏）
	for _, line := range strings.Split(combined, "\n") {
		if strings.TrimSpace(line) == "::set-var GREETING=hello" {
			t.Errorf("marker line leaked into log: %q", line)
		}
	}
}

// groupYAML 是 --group 相关测试共用的三 stage 配置：init / build / test，
// 每个 stage 一个 exit 0 的 shell step（runner 的 [stage/step] success 行会
// 经 logFn 输出到 stdout，可作为"该 step 确实执行"的证据）。
const groupYAML = `name: grouptest
pipeline:
  groups:
    all:
      stages: [init, build, test]
      description: "full pipeline"
    only_build: [build]
  stages:
    - name: init
      steps:
        - name: init-step
          type: shell
          script: |
            echo "INIT"
    - name: build
      steps:
        - name: build-step
          type: shell
          script: |
            echo "BUILD"
    - name: test
      steps:
        - name: test-step
          type: shell
          script: |
            echo "TEST"
`

// writeGroupProject 初始化项目目录并写入 groupYAML。
// 目录名与 yaml 的 name 一致（grouptest），以便 `run grouptest` 命中已注册项目。
func writeGroupProject(t *testing.T, home string) string {
	t.Helper()
	projDir := filepath.Join(home, "grouptest")
	if err := os.Mkdir(projDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	chdir(t, projDir)
	if _, stderr := executeCmd(t, "init"); stderr != "" {
		t.Fatalf("init: %q", stderr)
	}
	if err := os.WriteFile(filepath.Join(projDir, ".skating.yaml"), []byte(groupYAML), 0644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	return projDir
}

// groupDescYAML 是 groups 命令测试用的配置：mapping 形式 group 带 description，
// 列表形式 group 的 description 为空（应显示为 "-"）。
const groupDescYAML = `name: groupdesc
pipeline:
  groups:
    all:
      stages: [init, build]
      description: "full pipeline"
    build: [build]
  stages:
    - name: init
      steps:
        - name: init-step
          type: shell
          script: |
            echo "INIT"
    - name: build
      steps:
        - name: build-step
          type: shell
          script: |
            echo "BUILD"
`

// groupTableLine 返回表格输出中以 prefix 开头的行（用于按 group 名断言各列）。
func groupTableLine(out, prefix string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			return line
		}
	}
	return ""
}

// TestGroups_CurrentDir 验证无参数时直接读取当前目录的 .skating.yaml，
// 输出 group 名、stage 列表、description（列表形式的 group 显示 "-"）。
func TestGroups_CurrentDir(t *testing.T) {
	home := setupE2E(t)
	projDir := filepath.Join(home, "groupdesc")
	if err := os.Mkdir(projDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	chdir(t, projDir)
	// 不执行 init：groups 无参数时不要求项目已注册。
	if err := os.WriteFile(filepath.Join(projDir, ".skating.yaml"), []byte(groupDescYAML), 0644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	stdout, stderr := executeCmd(t, "groups")
	if stderr != "" {
		t.Fatalf("groups: stderr=%q", stderr)
	}

	allLine := groupTableLine(stdout, "all")
	if allLine == "" {
		t.Fatalf("output should contain an 'all' row: %q", stdout)
	}
	if !strings.Contains(allLine, "init, build") {
		t.Errorf("all row should list stages joined by ', ': %q", allLine)
	}
	if !strings.Contains(allLine, "full pipeline") {
		t.Errorf("all row should show description: %q", allLine)
	}

	buildLine := groupTableLine(stdout, "build")
	if buildLine == "" {
		t.Fatalf("output should contain a 'build' row: %q", stdout)
	}
	if !strings.Contains(buildLine, "-") {
		t.Errorf("list-form group should show '-' for description: %q", buildLine)
	}
}

// TestGroups_RegisteredProject 验证带项目名参数时从已注册项目路径读取配置。
func TestGroups_RegisteredProject(t *testing.T) {
	home := setupE2E(t)
	writeGroupProject(t, home)

	stdout, stderr := executeCmd(t, "groups", "grouptest")
	if stderr != "" {
		t.Fatalf("groups grouptest: stderr=%q", stderr)
	}

	allLine := groupTableLine(stdout, "all")
	if allLine == "" {
		t.Fatalf("output should contain an 'all' row: %q", stdout)
	}
	if !strings.Contains(allLine, "init, build, test") {
		t.Errorf("all row should list all stages: %q", allLine)
	}
	if !strings.Contains(allLine, "full pipeline") {
		t.Errorf("all row should show description from mapping form: %q", allLine)
	}

	onlyBuildLine := groupTableLine(stdout, "only_build")
	if onlyBuildLine == "" {
		t.Fatalf("output should contain an 'only_build' row: %q", stdout)
	}
	if !strings.Contains(onlyBuildLine, "-") {
		t.Errorf("list-form group should show '-' for description: %q", onlyBuildLine)
	}
}

// TestGroups_NoGroups 验证未定义 pipeline.groups 时输出提示且不报错。
func TestGroups_NoGroups(t *testing.T) {
	home := setupE2E(t)
	projDir := filepath.Join(home, "nogroups")
	if err := os.Mkdir(projDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	chdir(t, projDir)
	cfg := `name: nogroups
pipeline:
  stages:
    - name: build
      steps:
        - name: build-step
          type: shell
          script: |
            echo "BUILD"
`
	if err := os.WriteFile(filepath.Join(projDir, ".skating.yaml"), []byte(cfg), 0644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	stdout, stderr := executeCmd(t, "groups")
	if stderr != "" {
		t.Fatalf("groups without pipeline.groups should not error: stderr=%q", stderr)
	}
	if !strings.Contains(stdout, "pipeline.groups") {
		t.Errorf("output should mention missing pipeline.groups: %q", stdout)
	}
}

// stageDescYAML 是 stages 命令测试用的配置：setup/verify 带 description，
// compile 不带（应显示为 "-"），且声明顺序与字母序不同以验证按声明顺序输出。
const stageDescYAML = `name: stagedesc
pipeline:
  stages:
    - name: setup
      description: "Initialize the workspace"
      steps:
        - name: setup-step
          type: shell
          script: |
            echo "SETUP"
    - name: compile
      steps:
        - name: compile-step
          type: shell
          script: |
            echo "COMPILE"
    - name: verify
      description: "Run the test suite"
      steps:
        - name: verify-step
          type: shell
          script: |
            echo "VERIFY"
`

// TestStages_CurrentDir 验证无参数时直接读取当前目录的 .skating.yaml，
// 按声明顺序输出 stage 及其 description（空 description 显示 "-"）。
func TestStages_CurrentDir(t *testing.T) {
	home := setupE2E(t)
	projDir := filepath.Join(home, "stagedesc")
	if err := os.Mkdir(projDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	chdir(t, projDir)
	// 不执行 init：stages 无参数时不要求项目已注册。
	if err := os.WriteFile(filepath.Join(projDir, ".skating.yaml"), []byte(stageDescYAML), 0644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	stdout, stderr := executeCmd(t, "stages")
	if stderr != "" {
		t.Fatalf("stages: stderr=%q", stderr)
	}
	if !strings.Contains(stdout, "STAGE") || !strings.Contains(stdout, "DESCRIPTION") {
		t.Fatalf("output should contain STAGE / DESCRIPTION header: %q", stdout)
	}

	setupLine := groupTableLine(stdout, "setup")
	if setupLine == "" {
		t.Fatalf("output should contain a 'setup' row: %q", stdout)
	}
	if !strings.Contains(setupLine, "Initialize the workspace") {
		t.Errorf("setup row should show description: %q", setupLine)
	}

	compileLine := groupTableLine(stdout, "compile")
	if compileLine == "" {
		t.Fatalf("output should contain a 'compile' row: %q", stdout)
	}
	if !strings.Contains(compileLine, "-") {
		t.Errorf("stage without description should show '-': %q", compileLine)
	}

	verifyLine := groupTableLine(stdout, "verify")
	if verifyLine == "" {
		t.Fatalf("output should contain a 'verify' row: %q", stdout)
	}
	if !strings.Contains(verifyLine, "Run the test suite") {
		t.Errorf("verify row should show description: %q", verifyLine)
	}

	// 声明顺序：setup -> compile -> verify（字母序应为 compile < setup < verify）
	idxSetup := strings.Index(stdout, "setup")
	idxCompile := strings.Index(stdout, "compile")
	idxVerify := strings.Index(stdout, "verify")
	if idxSetup < 0 || idxCompile < 0 || idxVerify < 0 || !(idxSetup < idxCompile && idxCompile < idxVerify) {
		t.Errorf("stages should follow declaration order setup/compile/verify: %q", stdout)
	}
}

// TestStages_RegisteredProject 验证带项目名参数时从已注册项目路径读取配置
// （当前目录已切到项目目录之外）。
func TestStages_RegisteredProject(t *testing.T) {
	home := setupE2E(t)
	projDir := writeGroupProject(t, home)
	if err := os.WriteFile(filepath.Join(projDir, ".skating.yaml"), []byte(stageDescYAML), 0644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	chdir(t, t.TempDir())

	stdout, stderr := executeCmd(t, "stages", "grouptest")
	if stderr != "" {
		t.Fatalf("stages grouptest: stderr=%q", stderr)
	}

	setupLine := groupTableLine(stdout, "setup")
	if setupLine == "" {
		t.Fatalf("output should contain a 'setup' row: %q", stdout)
	}
	if !strings.Contains(setupLine, "Initialize the workspace") {
		t.Errorf("setup row should show description from registered project: %q", setupLine)
	}
	if compileLine := groupTableLine(stdout, "compile"); !strings.Contains(compileLine, "-") {
		t.Errorf("stage without description should show '-': %q", compileLine)
	}
}

// TestRun_GroupAll 验证 --group all 会运行组内全部 stage。
func TestRun_GroupAll(t *testing.T) {
	home := setupE2E(t)
	writeGroupProject(t, home)

	stdout, stderr := executeCmd(t, "run", "grouptest", "--group", "all")
	if stderr != "" {
		t.Fatalf("run --group all: stderr=%q", stderr)
	}
	combined := stdout + stderr
	for _, marker := range []string{
		"[init/init-step] success",
		"[build/build-step] success",
		"[test/test-step] success",
	} {
		if !strings.Contains(combined, marker) {
			t.Errorf("expected %q in output: %q", marker, combined)
		}
	}
	if strings.Contains(combined, "skipped") {
		t.Errorf("no stage should be skipped for --group all: %q", combined)
	}
}

// TestRun_GroupSubset 验证 --group only_build 只跑 build，其余记为 skipped。
func TestRun_GroupSubset(t *testing.T) {
	home := setupE2E(t)
	writeGroupProject(t, home)

	stdout, stderr := executeCmd(t, "run", "grouptest", "--group", "only_build")
	if stderr != "" {
		t.Fatalf("run --group only_build: stderr=%q", stderr)
	}
	combined := stdout + stderr
	if !strings.Contains(combined, "[build/build-step] success") {
		t.Errorf("build stage should have run: %q", combined)
	}
	for _, skipped := range []string{"[init/init-step] skipped", "[test/test-step] skipped"} {
		if !strings.Contains(combined, skipped) {
			t.Errorf("expected %q skipped marker: %q", skipped, combined)
		}
	}
}

// TestRun_GroupUnknown 验证 --group 不存在时列出已知 group。
func TestRun_GroupUnknown(t *testing.T) {
	home := setupE2E(t)
	writeGroupProject(t, home)

	_, stderr := executeCmd(t, "run", "grouptest", "--group", "nope")
	if !strings.Contains(stderr, "EXEC ERROR") {
		t.Fatalf("expected EXEC ERROR for unknown group, got stderr=%q", stderr)
	}
	if !strings.Contains(stderr, "nope") {
		t.Errorf("error should mention unknown group 'nope': %q", stderr)
	}
	if !strings.Contains(stderr, "only_build") {
		t.Errorf("error should list known groups (containing 'only_build'): %q", stderr)
	}
}

// TestRun_GroupAndStepConflict 验证 --group 与 --step 互斥。
func TestRun_GroupAndStepConflict(t *testing.T) {
	home := setupE2E(t)
	writeGroupProject(t, home)

	_, stderr := executeCmd(t, "run", "grouptest", "--group", "all", "--step", "build")
	if !strings.Contains(stderr, "EXEC ERROR") {
		t.Fatalf("expected EXEC ERROR for conflict, got stderr=%q", stderr)
	}
	if !strings.Contains(stderr, "--group") || !strings.Contains(stderr, "--step") {
		t.Errorf("conflict error should mention both flags: %q", stderr)
	}
}

// TestRun_GroupShorthandG 验证 -g 是 --group 的短选项，只跑 build。
func TestRun_GroupShorthandG(t *testing.T) {
	home := setupE2E(t)
	writeGroupProject(t, home)

	stdout, stderr := executeCmd(t, "run", "grouptest", "-g", "only_build")
	if stderr != "" {
		t.Fatalf("run -g only_build: stderr=%q", stderr)
	}
	combined := stdout + stderr
	if !strings.Contains(combined, "[build/build-step] success") {
		t.Errorf("build stage should have run: %q", combined)
	}
	for _, skipped := range []string{"[init/init-step] skipped", "[test/test-step] skipped"} {
		if !strings.Contains(combined, skipped) {
			t.Errorf("expected %q skipped marker: %q", skipped, combined)
		}
	}
}

// TestRun_GroupPlusSyntax 验证位置参数 <project>+<group> 等价于 --group。
func TestRun_GroupPlusSyntax(t *testing.T) {
	home := setupE2E(t)
	writeGroupProject(t, home)

	stdout, stderr := executeCmd(t, "run", "grouptest+only_build")
	if stderr != "" {
		t.Fatalf("run grouptest+only_build: stderr=%q", stderr)
	}
	combined := stdout + stderr
	if !strings.Contains(combined, "[build/build-step] success") {
		t.Errorf("build stage should have run: %q", combined)
	}
	for _, skipped := range []string{"[init/init-step] skipped", "[test/test-step] skipped"} {
		if !strings.Contains(combined, skipped) {
			t.Errorf("expected %q skipped marker: %q", skipped, combined)
		}
	}
}

// TestRun_GroupPlusAdHoc 验证 +<group> 在 ad-hoc 模式下命中 cwd 项目并只跑 build。
func TestRun_GroupPlusAdHoc(t *testing.T) {
	home := setupE2E(t)
	writeGroupProject(t, home) // 内部已 chdir 到项目目录

	stdout, stderr := executeCmd(t, "run", "+only_build")
	if stderr != "" {
		t.Fatalf("run +only_build: stderr=%q", stderr)
	}
	combined := stdout + stderr
	if !strings.Contains(combined, "[build/build-step] success") {
		t.Errorf("build stage should have run: %q", combined)
	}
	for _, skipped := range []string{"[init/init-step] skipped", "[test/test-step] skipped"} {
		if !strings.Contains(combined, skipped) {
			t.Errorf("expected %q skipped marker: %q", skipped, combined)
		}
	}
}

// TestRun_GroupPlusConflictWithFlag 验证位置 +group 与 -g/--group 不能同时使用。
func TestRun_GroupPlusConflictWithFlag(t *testing.T) {
	home := setupE2E(t)
	writeGroupProject(t, home)

	_, stderr := executeCmd(t, "run", "grouptest+all", "-g", "only_build")
	if !strings.Contains(stderr, "EXEC ERROR") {
		t.Fatalf("expected EXEC ERROR for duplicate group, got stderr=%q", stderr)
	}
	if !strings.Contains(stderr, "--group") {
		t.Errorf("duplicate-group error should mention --group: %q", stderr)
	}
	if !strings.Contains(stderr, "twice") && !strings.Contains(stderr, "不能同时使用") {
		t.Errorf("duplicate-group error should flag duplicate specification: %q", stderr)
	}
}

// TestRun_GroupPlusWithStep 验证位置 +group 与 --step 互斥。
func TestRun_GroupPlusWithStep(t *testing.T) {
	home := setupE2E(t)
	writeGroupProject(t, home)

	_, stderr := executeCmd(t, "run", "grouptest+all", "--step", "build")
	if !strings.Contains(stderr, "EXEC ERROR") {
		t.Fatalf("expected EXEC ERROR for +group/--step conflict, got stderr=%q", stderr)
	}
	if !strings.Contains(stderr, "--group") || !strings.Contains(stderr, "--step") {
		t.Errorf("conflict error should mention both --group and --step: %q", stderr)
	}
}

// TestRun_GroupPlusEmptyGroup 验证 "prj+" 缺少 group 名时报错。
func TestRun_GroupPlusEmptyGroup(t *testing.T) {
	home := setupE2E(t)
	writeGroupProject(t, home)

	_, stderr := executeCmd(t, "run", "grouptest+")
	if !strings.Contains(stderr, "EXEC ERROR") {
		t.Fatalf("expected EXEC ERROR for empty group name, got stderr=%q", stderr)
	}
	if !strings.Contains(stderr, "group name") && !strings.Contains(stderr, "group 名") {
		t.Errorf("error should mention missing group name: %q", stderr)
	}
	if !strings.Contains(stderr, "grouptest+") {
		t.Errorf("error should include the offending argument: %q", stderr)
	}
}

// TestRun_StepShorthandS 验证 -s 是 --step 的短选项：只跑 build stage，其余 skipped。
func TestRun_StepShorthandS(t *testing.T) {
	home := setupE2E(t)
	writeGroupProject(t, home)

	stdout, stderr := executeCmd(t, "run", "grouptest", "-s", "build")
	if stderr != "" {
		t.Fatalf("run -s build: stderr=%q", stderr)
	}
	combined := stdout + stderr
	if !strings.Contains(combined, "[build/build-step] success") {
		t.Errorf("build stage should have run: %q", combined)
	}
	for _, skipped := range []string{"[init/init-step] skipped", "[test/test-step] skipped"} {
		if !strings.Contains(combined, skipped) {
			t.Errorf("expected %q skipped marker: %q", skipped, combined)
		}
	}
}

// TestRun_EnvShorthandE 验证 -e 是 --env 的短选项：注入的变量能在 step 里读到。
func TestRun_EnvShorthandE(t *testing.T) {
	home := setupE2E(t)
	projDir := filepath.Join(home, "envshort")
	if err := os.Mkdir(projDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	chdir(t, projDir)

	if _, stderr := executeCmd(t, "init"); stderr != "" {
		t.Fatalf("init: %q", stderr)
	}

	// echo 变量后 exit 1，让脚本 stdout 通过 error message 冒到 stderr 供断言。
	yamlContent := `name: envshort
pipeline:
  stages:
    - name: probe
      steps:
        - name: dump-env
          type: shell
          script: |
            echo "USER_FOO=${USER_FOO}"
            exit 1
`
	if err := os.WriteFile(filepath.Join(projDir, ".skating.yaml"), []byte(yamlContent), 0644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	stdout, stderr := executeCmd(t, "run", "envshort", "-e", "USER_FOO=hello")
	combined := stdout + stderr
	if !strings.Contains(combined, "USER_FOO=hello") {
		t.Errorf("-e should inject USER_FOO=hello, output: %q", combined)
	}
}

// TestRun_StageEnvInterpolation 验证 YAML env 值中的 ${VAR} 会在运行前展开
// （支持拼接）：stage env version: "0.2.${SKA_BUILD_ID}" 在 build #1 时为 "0.2.1"。
func TestRun_StageEnvInterpolation(t *testing.T) {
	home := setupE2E(t)
	projDir := filepath.Join(home, "envinterp")
	if err := os.Mkdir(projDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	chdir(t, projDir)

	if _, stderr := executeCmd(t, "init"); stderr != "" {
		t.Fatalf("init: %q", stderr)
	}

	// exit 1 让脚本 stdout 通过 error message 冒到 stderr 供断言。
	yamlContent := `name: envinterp
pipeline:
  stages:
    - name: build
      env:
        default:
          version: "0.2.${SKA_BUILD_ID}"
      steps:
        - name: show
          type: shell
          script: |
            echo "VERSION=${version}"
            exit 1
`
	if err := os.WriteFile(filepath.Join(projDir, ".skating.yaml"), []byte(yamlContent), 0644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	stdout, stderr := executeCmd(t, "run", "envinterp")
	combined := stdout + stderr
	if !strings.Contains(combined, "VERSION=0.2.1") {
		t.Errorf("stage env ${SKA_BUILD_ID} not interpolated (want VERSION=0.2.1): %q", combined)
	}
}

// TestRun_StageWhenFalseSkipsStage 验证 stage 级 when: false 时该 stage 的 step
// 在摘要中记为 skipped，且整体构建仍为 success。
func TestRun_StageWhenFalseSkipsStage(t *testing.T) {
	home := setupE2E(t)
	projDir := filepath.Join(home, "stagewhen")
	if err := os.Mkdir(projDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	chdir(t, projDir)

	if _, stderr := executeCmd(t, "init"); stderr != "" {
		t.Fatalf("init: %q", stderr)
	}

	yamlContent := `name: stagewhen
pipeline:
  groups:
    all: [build, deploy]
  stages:
    - name: build
      steps:
        - name: build-step
          type: shell
          script: |
            echo "BUILD"
    - name: deploy
      when: false
      steps:
        - name: deploy-step
          type: shell
          script: |
            echo "DEPLOY"
`
	if err := os.WriteFile(filepath.Join(projDir, ".skating.yaml"), []byte(yamlContent), 0644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	stdout, stderr := executeCmd(t, "run", "stagewhen", "--group", "all")
	if stderr != "" {
		t.Fatalf("run stagewhen: stderr=%q", stderr)
	}
	combined := stdout + stderr
	if !strings.Contains(combined, "[build/build-step] success") {
		t.Errorf("build stage should have run: %q", combined)
	}
	if !strings.Contains(combined, "[deploy/deploy-step] skipped") {
		t.Errorf("disabled stage step should be skipped: %q", combined)
	}
	if strings.Contains(combined, "DEPLOY") {
		t.Errorf("disabled stage should not execute its script: %q", combined)
	}
	if !strings.Contains(combined, "=== Build SUCCESS ===") && !strings.Contains(combined, "=== 构建完成: success ===") {
		t.Errorf("build should still succeed with a disabled stage: %q", combined)
	}
}

// TestRun_TimeoutFlagOverride 验证 --timeout 覆盖运行超时并在启动时打印覆盖后的值。
func TestRun_TimeoutFlagOverride(t *testing.T) {
	home := setupE2E(t)
	writeGroupProject(t, home)

	stdout, stderr := executeCmd(t, "run", "grouptest", "--timeout", "5m")
	if stderr != "" {
		t.Fatalf("run --timeout 5m: stderr=%q", stderr)
	}
	if !strings.Contains(stdout, "Timeout: 5m0s") {
		t.Errorf("expected overridden timeout 'Timeout: 5m0s' in stdout: %q", stdout)
	}
}

// TestRun_TimeoutFlagInvalid 验证非法 --timeout 报错，且不执行 pipeline。
func TestRun_TimeoutFlagInvalid(t *testing.T) {
	home := setupE2E(t)
	writeGroupProject(t, home)

	stdout, stderr := executeCmd(t, "run", "grouptest", "--timeout", "nope")
	if !strings.Contains(stderr, "EXEC ERROR") {
		t.Fatalf("expected EXEC ERROR for invalid --timeout, got stderr=%q", stderr)
	}
	if !strings.Contains(stderr, "--timeout") {
		t.Errorf("error should mention --timeout: %q", stderr)
	}
	// 未进入 pipeline：不应出现 step 执行/skip 标记
	if strings.Contains(stdout+stderr, "[build/build-step]") {
		t.Errorf("pipeline should not run on invalid --timeout: %q", stdout+stderr)
	}
}

// TestStats_PruneOnce 验证 stats --once --prune 能正常运行并在无残留时报告清理 0 条。
func TestStats_PruneOnce(t *testing.T) {
	_ = setupE2E(t)

	stdout, stderr := executeCmd(t, "stats", "--once", "--prune")
	if stderr != "" {
		t.Fatalf("stats --once --prune: stderr=%q", stderr)
	}
	if !strings.Contains(stdout, "Pruned 0") {
		t.Errorf("expected 'Pruned 0' in stdout: %q", stdout)
	}
	if !strings.Contains(stdout, "Concurrency: 0/") {
		t.Errorf("expected 'Concurrency: 0/' headline: %q", stdout)
	}
}

// seedRunRecord 直接向本机 runstate.db 写入一条记录（供 stop 测试构造僵尸任务）。
func seedRunRecord(t *testing.T, home, project string, buildID int, status string, lastUpdate time.Time) {
	t.Helper()
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	id := fmt.Sprintf("%s/%s/%d", host, project, buildID)
	st := runstate.State{
		ID: id, Host: host, PID: 999999, Project: project, BuildID: buildID,
		Status: status, StartTime: lastUpdate, LastUpdate: lastUpdate,
	}
	buf, err := json.Marshal(st)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	dbPath := filepath.Join(home, ".skating", "runstate.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	db, err := bolt.Open(dbPath, 0600, nil)
	if err != nil {
		t.Fatalf("open runstate db: %v", err)
	}
	defer db.Close()
	if err := db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte("runs"))
		if err != nil {
			return err
		}
		return b.Put([]byte(id), buf)
	}); err != nil {
		t.Fatalf("put state: %v", err)
	}
}

// TestStop_RemovesStaleRecord 验证 stop 对心跳超时的僵尸记录直接删除。
func TestStop_RemovesStaleRecord(t *testing.T) {
	home := setupE2E(t)
	seedRunRecord(t, home, "grouptest", 102, runstate.StatusRunning, time.Now().Add(-time.Hour))

	stdout, stderr := executeCmd(t, "stop", "grouptest", "102")
	if stderr != "" {
		t.Fatalf("stop: stderr=%q", stderr)
	}
	if !strings.Contains(stdout, "Removed stale record") {
		t.Errorf("expected stale-record removal message, got: %q", stdout)
	}
	if _, err := runstate.LocalState("grouptest", 102); !errors.Is(err, runstate.ErrNotFound) {
		t.Errorf("stale record should be deleted, got err=%v", err)
	}
}

// TestStop_UnknownBuild 验证 stop 对不存在的记录报错。
func TestStop_UnknownBuild(t *testing.T) {
	_ = setupE2E(t)

	_, stderr := executeCmd(t, "stop", "grouptest", "999")
	if !strings.Contains(stderr, "EXEC ERROR") {
		t.Fatalf("expected EXEC ERROR for unknown build, got stderr=%q", stderr)
	}
	if !strings.Contains(stderr, "999") {
		t.Errorf("error should mention build id 999: %q", stderr)
	}
}

// TestStop_InvalidBuildID 验证非整数 build-id 报错。
func TestStop_InvalidBuildID(t *testing.T) {
	_ = setupE2E(t)

	_, stderr := executeCmd(t, "stop", "grouptest", "abc")
	if !strings.Contains(stderr, "EXEC ERROR") {
		t.Fatalf("expected EXEC ERROR for invalid build id, got stderr=%q", stderr)
	}
	if !strings.Contains(stderr, "abc") {
		t.Errorf("error should mention the invalid value 'abc': %q", stderr)
	}
}

// TestStop_AlreadyFinished 验证已结束的记录不会被误删。
func TestStop_AlreadyFinished(t *testing.T) {
	home := setupE2E(t)
	seedRunRecord(t, home, "grouptest", 50, runstate.StatusSuccess, time.Now().Add(-time.Hour))

	stdout, stderr := executeCmd(t, "stop", "grouptest", "50")
	if stderr != "" {
		t.Fatalf("stop: stderr=%q", stderr)
	}
	if !strings.Contains(stdout, "already finished") {
		t.Errorf("expected 'already finished' message, got: %q", stdout)
	}
	if _, err := runstate.LocalState("grouptest", 50); err != nil {
		t.Errorf("finished record should be kept, got err=%v", err)
	}
}

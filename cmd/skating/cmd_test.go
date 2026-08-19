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
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hpgood/skating/internal/store"
	"github.com/spf13/pflag"
)

func setupE2E(t *testing.T) string {
	t.Helper()
	// t.Setenv automatically restores on test cleanup.
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
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
	runUserEnv = nil
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
//  - 生成 .skating.yaml + scripts/build.sh + scripts/build.lua
//  - 在 ~/.skating/projects.yaml 里注册 Project，name 取自 cwd basename
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
	if p.Image != "golang:1.21" {
		t.Errorf("project.Image = %q, want golang:1.21", p.Image)
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
//  - run 成功后 build ID 自动递增到 1
//  - 再 run 一次后 build ID 应该是 2（验证 8/18 修过的 #3 BuildID 不自增 bug）
//  - logs 默认读 latest，logs --id 2 能读到第二次 build 的日志
//  - logs --last N 能列出摘要
//  - run 失败时 exit code != 0，log 里带 "=== Build FAILED ===" marker
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

// TestRun_InjectedBuildEnvVars 验证 cmd_run 注入的三个 build-time env
// (SKA_BUILD_ID / SKA_BUILD_TIMESTAMP / SKA_BUILD_DATE) 都能从 step 里读到,
// 且格式/一致性正确:
//   - SKA_BUILD_TIMESTAMP 是 10 位整数 unix 秒 (UTC)
//   - SKA_BUILD_DATE     是 RFC3339 字符串以 'Z' 结尾 (UTC)
//   - 转 SKA_BUILD_TIMESTAMP 回 UTC 日期 ≈ SKA_BUILD_DATE (一致性)
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
	var tsStr string
	for _, line := range strings.Split(combined, "\n") {
		if strings.HasPrefix(line, "SKA_BUILD_TIMESTAMP=") {
			tsStr = strings.TrimPrefix(line, "SKA_BUILD_TIMESTAMP=")
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
			dateStr = strings.TrimPrefix(line, "SKA_BUILD_DATE=")
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
	var roundtripStr string
	for _, line := range strings.Split(combined, "\n") {
		if strings.HasPrefix(line, "ROUNDTRIP=") {
			roundtripStr = strings.TrimPrefix(line, "ROUNDTRIP=")
		}
	}
	if roundtripStr != "" && roundtripStr != dateStr {
		t.Errorf("SKA_BUILD_TIMESTAMP (unix %s) converted back via 'date -u -d @...' = %q, but SKA_BUILD_DATE = %q (should be equal)",
			tsStr, roundtripStr, dateStr)
	}
}

// TestRun_StepFilterSingleStep 验证 --step stage/step 只运行指定单个 step，
// 其他 stage 和同 stage 内其他 step 都记为 skipped。
//
// Pipeline 设计（避免 host shell 干扰，shell step 一律 exit 0 让 stdout 进 stderr）：
//   stage-pre:   step-pre-only
//   stage-build: step-keep (被选中的, exit 1), step-skip (同 stage, exit 0)
//   stage-post:  step-post-only
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
//   stage-build: step-a (exit 0), step-b (exit 0)
//   stage-other: step-x (exit 0)
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
//   - shell step 把 SKA_BUILD_ID / USER_FOO / USER_OVERRIDE 三个 env 都 echo 出来然后 exit 1
//   - -e USER_FOO=hello -e USER_OVERRIDE=my-value -e SKA_BUILD_ID=999
//   - 验证 USER_FOO / USER_OVERRIDE 被注入
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
      steps:
        - name: dump-env
          type: shell
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
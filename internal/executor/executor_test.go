package executor

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestMain 在测试期间默认禁用 docker，避免 host 有 docker CLI 时意外走 docker 路径
// 个别需要 docker 的测试可以单独覆盖
func TestMain(m *testing.M) {
	if os.Getenv("SKATING_ENABLE_DOCKER_IN_TESTS") != "1" {
		os.Setenv("SKATING_DISABLE_DOCKER", "1")
	}
	os.Exit(m.Run())
}

func TestRunner_ExecuteShell_InlineScript(t *testing.T) {
	r := NewRunner()
	step := Step{
		Name:   "echo",
		Type:   "shell",
		Script: "echo hello-from-shell",
	}
	out, err := r.Execute(step, t.TempDir(), nil, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "hello-from-shell") {
		t.Errorf("output = %q, want contains 'hello-from-shell'", out)
	}
}

func TestRunner_ExecuteShell_EnvInjection(t *testing.T) {
	r := NewRunner()
	step := Step{
		Name:   "env-check",
		Type:   "shell",
		Script: "echo MY_VAR=$MY_VAR",
	}
	out, err := r.Execute(step, t.TempDir(), map[string]string{"MY_VAR": "injected"}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "MY_VAR=injected") {
		t.Errorf("env not injected: %q", out)
	}
}

func TestRunner_ExecuteShell_FailureReturnsError(t *testing.T) {
	r := NewRunner()
	step := Step{
		Name:   "fail",
		Type:   "shell",
		Script: "exit 42",
	}
	_, err := r.Execute(step, t.TempDir(), nil, nil)
	if err == nil {
		t.Fatal("expected error from exit 42")
	}
	if !strings.Contains(err.Error(), "exit") {
		t.Errorf("error should mention exit, got: %v", err)
	}
}

func TestRunner_ExecuteShell_NativeCommandFailurePropagates(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("powershell-specific behavior")
	}
	r := NewRunner()
	step := Step{
		Name:   "native-fail",
		Type:   "shell",
		Script: "cmd /c exit 7",
	}
	if _, err := r.Execute(step, t.TempDir(), nil, nil); err == nil {
		t.Fatal("expected native command failure to propagate via exit $LASTEXITCODE")
	}
}

func TestRunner_ExecuteShell_PureCmdletStillSucceeds(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("powershell-specific behavior")
	}
	r := NewRunner()
	step := Step{
		Name:   "cmdlet-ok",
		Type:   "shell",
		Script: "Write-Output cmdlet-ok",
	}
	if _, err := r.Execute(step, t.TempDir(), nil, nil); err != nil {
		t.Fatalf("pure cmdlet script should still pass: %v", err)
	}
}

func TestRunner_ExecuteShell_WorkdirIsHonored(t *testing.T) {
	r := NewRunner()
	dir := t.TempDir()
	// 用标记文件验证 workdir：部分环境下 t.TempDir 返回 8.3 短路径（如 ADMINI~1），
	// 而脚本里拿到的 cwd 是长路径，直接比较字符串不可靠
	marker := "skating-workdir-marker"
	if err := os.WriteFile(filepath.Join(dir, marker), nil, 0644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	var script string
	if runtime.GOOS == "windows" {
		script = fmt.Sprintf("if (Test-Path -LiteralPath './%s') { Write-Output WORKDIR-OK }", marker)
	} else {
		script = fmt.Sprintf("test -f ./%s && echo WORKDIR-OK", marker)
	}
	step := Step{
		Name:   "pwd",
		Type:   "shell",
		Script: script,
	}
	out, err := r.Execute(step, dir, nil, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// 逐行精确匹配：PowerShell/bash 的 DEBUG/trace 行含脚本文本但不等于整行
	found := false
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "WORKDIR-OK" {
			found = true
		}
	}
	if !found {
		t.Errorf("workdir not honored: output=%q", out)
	}
}

func TestRunner_ExecuteShell_UnknownType(t *testing.T) {
	r := NewRunner()
	step := Step{Name: "x", Type: "python", Script: "pass"}
	_, err := r.Execute(step, t.TempDir(), nil, nil)
	if err == nil {
		t.Fatal("expected error for unknown step type")
	}
}

func TestRunner_LuaSandbox_DisallowsOs(t *testing.T) {
	r := NewRunner()
	step := Step{
		Name:   "lua-os",
		Type:   "lua",
		Script: `os.execute("echo should-not-run")`,
	}
	_, err := r.Execute(step, t.TempDir(), nil, nil)
	if err == nil {
		t.Fatal("expected error: os module should be disabled in Lua sandbox")
	}
}

func TestRunner_LuaSandbox_DisallowsIo(t *testing.T) {
	r := NewRunner()
	step := Step{
		Name:   "lua-io",
		Type:   "lua",
		Script: `io.open("/etc/passwd", "r")`,
	}
	_, err := r.Execute(step, t.TempDir(), nil, nil)
	if err == nil {
		t.Fatal("expected error: io module should be disabled in Lua sandbox")
	}
}

func TestRunner_LuaSandbox_AllowsPrint(t *testing.T) {
	r := NewRunner()
	step := Step{
		Name:   "lua-print",
		Type:   "lua",
		Script: `print("hello from lua")`,
	}
	if _, err := r.Execute(step, t.TempDir(), nil, nil); err != nil {
		t.Errorf("print should work, got: %v", err)
	}
}

func TestRunner_LuaSandbox_ShAPIReusesEnv(t *testing.T) {
	// 关键回归测试：之前 Lua sh() 会用空 Runner，导致 env 丢失
	// 验证：Lua 脚本里能用 env 中的变量、sh 调用本身不报错
	r := NewRunner()
	// 通过 set_env 注入，再通过 sh 读取 → 确认 Runner 跨 API 调用复用同一份 env
	step := Step{
		Name: "lua-sh-env",
		Type: "lua",
		Script: `
set_env("FROM_LUA_SH", "inherited")
local out, err = sh("echo FROM_LUA_SH=$FROM_LUA_SH")
if err then error(err) end
if not string.find(out, "FROM_LUA_SH=inherited") then
  error("sh() did not see env: got " .. out)
end
print("LuaShEnvOK")
`,
	}
	out, err := r.Execute(step, t.TempDir(), nil, nil)
	if err != nil {
		t.Fatalf("Execute: %v\noutput: %q", err, out)
	}
	if !strings.Contains(out, "LuaShEnvOK") {
		t.Errorf("expected LuaShEnvOK marker, got: %q", out)
	}
}

func TestRunner_LuaSandbox_ErrorIncludesStepName(t *testing.T) {
	r := NewRunner()
	step := Step{
		Name:   "my-lua-step",
		Type:   "lua",
		Script: `error("boom")`,
	}
	_, err := r.Execute(step, t.TempDir(), nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "my-lua-step") {
		t.Errorf("error should include step name 'my-lua-step', got: %v", err)
	}
}

func TestRunner_NoDocker_UsesHostShell(t *testing.T) {
	// step 配置了 docker image 但 docker 不可用时，应降级到 host shell
	r := NewRunner()
	// 即使 step 配置了镜像，host shell 也能跑（取决于 IsDockerAvailable）
	step := Step{
		Name:   "host-fallback",
		Type:   "shell",
		Image:  "nonexistent:never",
		Script: "echo fallback-ok",
	}
	out, err := r.Execute(step, t.TempDir(), nil, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "fallback-ok") {
		t.Errorf("host fallback should work, got: %q", out)
	}
}

func TestRunner_ExecuteShell_NeitherScriptNorSource(t *testing.T) {
	r := NewRunner()
	step := Step{Name: "empty", Type: "shell"}
	_, err := r.Execute(step, t.TempDir(), nil, nil)
	if err == nil {
		t.Fatal("expected error for empty step")
	}
}

func TestRunner_ExecuteLua_NeitherScriptNorSource(t *testing.T) {
	r := NewRunner()
	step := Step{Name: "empty-lua", Type: "lua"}
	_, err := r.Execute(step, t.TempDir(), nil, nil)
	if err == nil {
		t.Fatal("expected error for empty lua step")
	}
}

func TestRunner_ExecuteShell_SourceFile(t *testing.T) {
	dir := t.TempDir()
	// Windows 用 PowerShell 执行，要求 .ps1 扩展名；脚本语法也按平台切换
	scriptPath := dir + "/script.sh"
	content := "#!/bin/bash\necho from-source\n"
	if runtime.GOOS == "windows" {
		scriptPath = dir + "/script.ps1"
		content = "Write-Output \"from-source\"\n"
	}
	if err := writeFile(scriptPath, content); err != nil {
		t.Fatalf("write script: %v", err)
	}
	r := NewRunner()
	step := Step{Name: "src", Type: "shell", Source: scriptPath}
	out, err := r.Execute(step, dir, nil, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "from-source") {
		t.Errorf("source execution failed: %q", out)
	}
}

// writeFile 简化 os.WriteFile 调用
func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0755)
}

// buildDockerRunArgs 应在内置参数之后追加用户 docker_options，
// 且不包含镜像名（镜像由调用方追加）
func TestBuildDockerRunArgs_UserOptionsAppended(t *testing.T) {
	step := Step{
		Name:          "pack",
		Script:        "npm run build",
		DockerOptions: []string{"--network", "host", "-v", "/tmp/cache:/var/cache"},
	}
	env := map[string]string{"GOOS": "linux", "Ext": ""}

	args := buildDockerRunArgs(step, "skating-123", "C:/work", "C:/tmp/script.sh", "/tmp/skating-script.sh", env)

	// 内置前置段
	if args[0] != "run" || args[1] != "--rm" {
		t.Fatalf("args head = %v, want [run --rm ...]", args[:2])
	}
	// 用户 options 应位于最后（调用方在其后追加镜像名）
	wantTail := []string{"--network", "host", "-v", "/tmp/cache:/var/cache"}
	gotTail := args[len(args)-len(wantTail):]
	for i := range wantTail {
		if gotTail[i] != wantTail[i] {
			t.Fatalf("tail = %v, want %v", gotTail, wantTail)
		}
	}
	// 内置 -w /work（默认容器工作目录）存在
	if !contains(args, "-w") || !contains(args, "/work") {
		t.Errorf("built-in -w /work missing: %v", args)
	}
	// env 按序注入
	if !contains(args, "-e") || !contains(args, "Ext=") || !contains(args, "GOOS=linux") {
		t.Errorf("env injection missing: %v", args)
	}
	// 内联脚本额外挂载存在
	if !contains(args, "C:/tmp/script.sh:/tmp/skating-script.sh") {
		t.Errorf("script mount missing: %v", args)
	}
	// 镜像名不应出现在前置参数中
	if contains(args, "golang:1.25") {
		t.Errorf("image must not be in pre-image args: %v", args)
	}
}

// 无用户 options 时，前置参数以内置段结尾（内联脚本挂载在 -w 之后）
func TestBuildDockerRunArgs_NoUserOptions(t *testing.T) {
	step := Step{Name: "compile", Script: "go build"}
	args := buildDockerRunArgs(step, "skating-1", "C:/work", "C:/tmp/s.sh", "/tmp/skating-script.sh", nil)
	wantLast := "C:/tmp/s.sh:/tmp/skating-script.sh"
	if len(args) == 0 || args[len(args)-1] != wantLast {
		t.Fatalf("last built-in arg should be script mount %q, got %v", wantLast, args)
	}
}

// step.workdir 自定义容器内工作目录：挂载点与 -w 同步，自动补 / 前缀
func TestBuildDockerRunArgs_CustomWorkDir(t *testing.T) {
	step := Step{Name: "build", Script: "go build", WorkDir: "gocache"}
	args := buildDockerRunArgs(step, "skating-1", "C:/work", "C:/tmp/s.sh", "/tmp/skating-script.sh", nil)
	if !contains(args, "C:/work:/gocache") || !contains(args, "-w") || !contains(args, "/gocache") {
		t.Errorf("custom workdir not applied (want mount C:/work:/gocache and -w /gocache): %v", args)
	}

	// 以 / 开头时保持原样
	step.WorkDir = "/custom/dir"
	args = buildDockerRunArgs(step, "skating-1", "C:/work", "C:/tmp/s.sh", "/tmp/skating-script.sh", nil)
	if !contains(args, "C:/work:/custom/dir") || !contains(args, "/custom/dir") {
		t.Errorf("absolute workdir not preserved: %v", args)
	}
}

func contains(arr []string, s string) bool {
	for _, v := range arr {
		if v == s {
			return true
		}
	}
	return false
}

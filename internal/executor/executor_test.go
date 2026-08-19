package executor

import (
	"os"
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
	out, err := r.Execute(step, t.TempDir(), nil)
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
	out, err := r.Execute(step, t.TempDir(), map[string]string{"MY_VAR": "injected"})
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
	_, err := r.Execute(step, t.TempDir(), nil)
	if err == nil {
		t.Fatal("expected error from exit 42")
	}
	if !strings.Contains(err.Error(), "exit") {
		t.Errorf("error should mention exit, got: %v", err)
	}
}

func TestRunner_ExecuteShell_WorkdirIsHonored(t *testing.T) {
	r := NewRunner()
	dir := t.TempDir()
	step := Step{
		Name:   "pwd",
		Type:   "shell",
		Script: "pwd",
	}
	out, err := r.Execute(step, dir, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// t.TempDir 返回的是绝对路径
	if !strings.Contains(out, dir) {
		t.Errorf("workdir not honored. dir=%s, output=%q", dir, out)
	}
}

func TestRunner_ExecuteShell_UnknownType(t *testing.T) {
	r := NewRunner()
	step := Step{Name: "x", Type: "python", Script: "pass"}
	_, err := r.Execute(step, t.TempDir(), nil)
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
	_, err := r.Execute(step, t.TempDir(), nil)
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
	_, err := r.Execute(step, t.TempDir(), nil)
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
	if _, err := r.Execute(step, t.TempDir(), nil); err != nil {
		t.Errorf("print should work, got: %v", err)
	}
}

func TestRunner_LuaSandbox_ShAPIReusesEnv(t *testing.T) {
	// 关键回归测试：之前 Lua sh() 会用空 Runner，导致 env 丢失
	// 验证：Lua 脚本里能用 env 中的变量、sh 调用本身不报错
	r := NewRunner()
	// 通过 set_env 注入，再通过 sh 读取 → 确认 Runner 跨 API 调用复用同一份 env
	step := Step{
		Name:   "lua-sh-env",
		Type:   "lua",
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
	out, err := r.Execute(step, t.TempDir(), nil)
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
	_, err := r.Execute(step, t.TempDir(), nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "my-lua-step") {
		t.Errorf("error should include step name 'my-lua-step', got: %v", err)
	}
}

func TestRunner_SetDockerImage_Available(t *testing.T) {
	r := NewRunner()
	r.SetDockerImage("golang:1.25")
	if r.DockerImage() != "golang:1.25" {
		t.Errorf("docker image not set")
	}
	// IsDockerAvailable 取决于 host 是否有 docker；只断言方法不 panic
	_ = r.IsDockerAvailable()
}

func TestRunner_NoDocker_UsesHostShell(t *testing.T) {
	// 配置 docker image 但 docker 不可用时，应降级到 host shell
	r := NewRunner()
	r.SetDockerImage("nonexistent:never")
	// 即使 docker 配置了，host shell 也能跑（取决于 IsDockerAvailable）
	step := Step{
		Name:   "host-fallback",
		Type:   "shell",
		Script: "echo fallback-ok",
	}
	out, err := r.Execute(step, t.TempDir(), nil)
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
	_, err := r.Execute(step, t.TempDir(), nil)
	if err == nil {
		t.Fatal("expected error for empty step")
	}
}

func TestRunner_ExecuteLua_NeitherScriptNorSource(t *testing.T) {
	r := NewRunner()
	step := Step{Name: "empty-lua", Type: "lua"}
	_, err := r.Execute(step, t.TempDir(), nil)
	if err == nil {
		t.Fatal("expected error for empty lua step")
	}
}

func TestRunner_ExecuteShell_SourceFile(t *testing.T) {
	dir := t.TempDir()
	scriptPath := dir + "/script.sh"
	if err := writeFile(scriptPath, "#!/bin/bash\necho from-source\n"); err != nil {
		t.Fatalf("write script: %v", err)
	}
	r := NewRunner()
	step := Step{Name: "src", Type: "shell", Source: scriptPath}
	out, err := r.Execute(step, dir, nil)
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
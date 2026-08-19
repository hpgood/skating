package plugin

import (
	"errors"
	"strings"
	"testing"
)

type fakePlugin struct {
	name    string
	version string
	initErr error
	runErr  error
	calls   int
}

func (f *fakePlugin) Name() string    { return f.name }
func (f *fakePlugin) Version() string { return f.version }
func (f *fakePlugin) Init() error     { return f.initErr }
func (f *fakePlugin) Run(ctx *Context) error {
	f.calls++
	return f.runErr
}

// 注意：本测试修改全局 plugin.registry，依赖 -p 1（顺序执行）保证不互相干扰
// 并在 cleanup 中清空

func TestRegisterAndGetPlugins(t *testing.T) {
	cleanup := resetRegistry()
	defer cleanup()

	// resetRegistry 后只剩内置 ConsoleNotifier
	if len(GetPlugins()) != 1 {
		t.Fatalf("expected 1 builtin plugin after reset, got %d", len(GetPlugins()))
	}

	RegisterPlugin(&fakePlugin{name: "a", version: "1.0"})
	RegisterPlugin(&fakePlugin{name: "b", version: "2.0"})

	plugins := GetPlugins()
	if len(plugins) != 3 {
		t.Fatalf("expected 3 plugins (builtin + 2), got %d", len(plugins))
	}
}

func TestRegisterPlugin_OverwritesOnDuplicateName(t *testing.T) {
	cleanup := resetRegistry()
	defer cleanup()

	p1 := &fakePlugin{name: "dup", version: "1.0"}
	p2 := &fakePlugin{name: "dup", version: "2.0"}

	RegisterPlugin(p1)
	RegisterPlugin(p2)

	// 期望: dup + ConsoleNotifier = 2 个插件
	plugins := GetPlugins()
	if len(plugins) != 2 {
		t.Errorf("expected 2 plugins (builtin + 1 dedup'd), got %d", len(plugins))
	}

	// 找 dup 那条，应该是 2.0
	var dupPlugin Plugin
	for _, p := range plugins {
		if p.Name() == "dup" {
			dupPlugin = p
			break
		}
	}
	if dupPlugin == nil {
		t.Fatal("dup plugin not found")
	}
	if dupPlugin.Version() != "2.0" {
		t.Errorf("expected version 2.0 (latest), got %s", dupPlugin.Version())
	}
}

func TestRunPlugins_ContextPassedCorrectly(t *testing.T) {
	cleanup := resetRegistry()
	defer cleanup()

	p := &capturePlugin{}
	RegisterPlugin(p)

	ctx := &Context{
		ProjectName: "demo",
		BuildID:     42,
		Status:      "success",
		Duration:    "1.5s",
		Output:      "log content",
	}
	RunPlugins(ctx)

	if p.received == nil {
		t.Fatal("plugin Run not called")
	}
	if p.received.ProjectName != "demo" {
		t.Errorf("ProjectName = %q, want demo", p.received.ProjectName)
	}
	if p.received.BuildID != 42 {
		t.Errorf("BuildID = %d, want 42", p.received.BuildID)
	}
	if p.received.Status != "success" {
		t.Errorf("Status = %q", p.received.Status)
	}
	if p.received.Output != "log content" {
		t.Errorf("Output not passed through")
	}
}

func TestRunPlugins_AggregatesErrors(t *testing.T) {
	cleanup := resetRegistry()
	defer cleanup()

	RegisterPlugin(&fakePlugin{name: "ok", runErr: nil})
	RegisterPlugin(&fakePlugin{name: "fail1", runErr: errors.New("boom1")})
	RegisterPlugin(&fakePlugin{name: "fail2", runErr: errors.New("boom2")})

	errs := RunPlugins(&Context{ProjectName: "x"})

	if len(errs) != 2 {
		t.Fatalf("expected 2 errors, got %d", len(errs))
	}
	combined := strings.Join([]string{errs[0].Error(), errs[1].Error()}, "|")
	if !strings.Contains(combined, "boom1") || !strings.Contains(combined, "boom2") {
		t.Errorf("expected both errors aggregated, got %q", combined)
	}
}

// capturePlugin 记录收到的 ctx 用于验证
type capturePlugin struct {
	received *Context
}

func (c *capturePlugin) Name() string    { return "capture" }
func (c *capturePlugin) Version() string { return "1.0" }
func (c *capturePlugin) Init() error     { return nil }
func (c *capturePlugin) Run(ctx *Context) error {
	c.received = ctx
	return nil
}

// resetRegistry 清空全局 registry 后只放回内置 ConsoleNotifier
// 测试结束后再次清空（保持测试隔离）
func resetRegistry() func() {
	registry = make(map[string]Plugin)
	RegisterPlugin(&ConsoleNotifier{})
	return func() {
		registry = make(map[string]Plugin)
	}
}
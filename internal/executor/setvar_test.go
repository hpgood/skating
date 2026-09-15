package executor

import (
	"strings"
	"testing"
)

func TestParseSetVarLine(t *testing.T) {
	cases := []struct {
		line  string
		key   string
		value string
		ok    bool
	}{
		{"::set-var FOO=bar", "FOO", "bar", true},
		{"  ::set-var FOO=bar\t", "FOO", "bar", true},      // 行首尾空白
		{"::set-var FOO=bar=baz", "FOO", "bar=baz", true},  // VALUE 含 =
		{"::set-var EMPTY=", "EMPTY", "", true},            // 空 VALUE
		{"::set-var FOO=bar\r", "FOO", "bar", true},        // CRLF 残留
		{"::set-var =novalue", "", "", false},              // 空 KEY
		{"::set-var FOO", "", "", false},                   // 缺 =
		{"::set-var", "", "", false},                       // 无 KV
		{"hello world", "", "", false},                     // 普通行
		{"::set-other FOO=bar", "", "", false},             // 前缀不同
		{"echo ::set-var FOO=bar", "", "", false},          // 标记不在行首
		{"DEBUG: echo \"::set-var FOO=bar\"", "", "", false}, // PS trace 行
	}
	for _, c := range cases {
		k, v, ok := parseSetVarLine(c.line)
		if ok != c.ok || k != c.key || v != c.value {
			t.Errorf("parseSetVarLine(%q) = (%q, %q, %v), want (%q, %q, %v)",
				c.line, k, v, ok, c.key, c.value, c.ok)
		}
	}
}

func TestLineStreamer_SetVarCaptured(t *testing.T) {
	var got []string
	var logged []string
	ls := &lineStreamer{
		logFn:    func(msg string) { logged = append(logged, msg) },
		onSetVar: func(k, v string) { got = append(got, k+"="+v) },
	}
	ls.Write([]byte("normal line\n"))
	ls.Write([]byte("::set-var FOO=bar\n"))
	ls.Write([]byte("::set-var BAZ=qux=1\nanother\n"))
	ls.flush()

	wantVars := []string{"FOO=bar", "BAZ=qux=1"}
	if strings.Join(got, ",") != strings.Join(wantVars, ",") {
		t.Errorf("captured vars = %v, want %v", got, wantVars)
	}

	// 标记行不进累积输出，普通行原样保留
	if out := ls.String(); out != "normal line\nanother\n" {
		t.Errorf("output = %q, want %q", out, "normal line\nanother\n")
	}
	// 标记行不进日志
	for _, l := range logged {
		if strings.Contains(l, "::set-var") {
			t.Errorf("marker line leaked into log: %q", l)
		}
	}
}

func TestLineStreamer_MarkerSwallowedWithoutCallback(t *testing.T) {
	// onSetVar 为 nil 时协议行同样被捕获丢弃，防止污染日志与返回值
	ls := &lineStreamer{}
	ls.Write([]byte("::set-var FOO=bar\nplain\n"))
	ls.flush()
	if out := ls.String(); out != "plain\n" {
		t.Errorf("output = %q, want %q (marker swallowed)", out, "plain\n")
	}
}

// TestRunner_ExecuteShell_SetVarCallback 真实 shell 路径：脚本 echo 标记行
// 触发回调，且标记行不进返回输出（含 PowerShell DEBUG trace 行在内的
// 其他行不受影响）。
func TestRunner_ExecuteShell_SetVarCallback(t *testing.T) {
	r := NewRunner()
	got := map[string]string{}
	r.OnSetVar(func(k, v string) { got[k] = v })

	step := Step{
		Name:   "setvar",
		Type:   "shell",
		Script: "echo \"::set-var FOO=bar\"\necho done",
	}
	out, err := r.Execute(step, t.TempDir(), nil, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got["FOO"] != "bar" {
		t.Errorf("callback got %v, want FOO=bar", got)
	}
	// 标记行不进返回输出（逐行检查：PS DEBUG trace 行含标记文本但前缀不同，不算泄漏）
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "::set-var ") {
			t.Errorf("marker line leaked into output: %q", out)
		}
	}
	if !strings.Contains(out, "done") {
		t.Errorf("normal output lost: %q", out)
	}
}

// TestRunner_ExecuteLua_SetEnvCallback 验证 Lua set_env 触发跨 step 回调
func TestRunner_ExecuteLua_SetEnvCallback(t *testing.T) {
	r := NewRunner()
	got := map[string]string{}
	r.OnSetVar(func(k, v string) { got[k] = v })

	step := Step{
		Name:   "lua-set",
		Type:   "lua",
		Script: `set_env("LUA_VAR", "from-lua")`,
	}
	if _, err := r.Execute(step, t.TempDir(), nil, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got["LUA_VAR"] != "from-lua" {
		t.Errorf("callback got %v, want LUA_VAR=from-lua", got)
	}
}

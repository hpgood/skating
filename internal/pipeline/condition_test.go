package pipeline

import (
	"os/exec"
	"runtime"
	"testing"
)

func TestEvalWhen_EmptyCondition(t *testing.T) {
	ok, err := EvalWhen("", nil, nil)
	if err != nil {
		t.Errorf("empty condition should not error: %v", err)
	}
	if !ok {
		t.Error("empty condition should be true")
	}
}

func TestEvalWhen_NoOperator(t *testing.T) {
	_, err := EvalWhen("foo bar", nil, nil)
	if err == nil {
		t.Error("expected error for missing operator")
	}
}

func TestEvalWhen_NumericComparison(t *testing.T) {
	tests := []struct {
		expr string
		want bool
	}{
		{"1 == 1", true},
		{"1 == 2", false},
		{"1 != 2", true},
		{"3 > 2", true},
		{"2 > 3", false},
		{"2 < 3", true},
		{"3 < 2", false},
		{"3 >= 3", true},
		{"2 >= 3", false},
		{"3 <= 3", true},
		{"4 <= 3", false},
	}
	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			ok, err := EvalWhen(tc.expr, nil, nil)
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if ok != tc.want {
				t.Errorf("%s = %v, want %v", tc.expr, ok, tc.want)
			}
		})
	}
}

func TestEvalWhen_StringComparison(t *testing.T) {
	tests := []struct {
		expr string
		want bool
	}{
		{`"main" == "main"`, true},
		{`"main" == "dev"`, false},
		{`"main" != "dev"`, true},
	}
	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			ok, err := EvalWhen(tc.expr, nil, nil)
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if ok != tc.want {
				t.Errorf("%s = %v, want %v", tc.expr, ok, tc.want)
			}
		})
	}
}

func TestEvalWhen_EnvVarResolution(t *testing.T) {
	env := map[string]string{"SKA_BUILD_ID": "5"}
	ok, err := EvalWhen("$SKA_BUILD_ID == 5", env, nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !ok {
		t.Error("$VAR should resolve from env")
	}
}

func TestEvalWhen_EnvVarMissing(t *testing.T) {
	env := map[string]string{}
	// 不存在的 env 变量 → 空字符串
	ok, err := EvalWhen("$MISSING == \"\"", env, nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !ok {
		t.Error("missing env var should resolve to empty string")
	}
}

func TestEvalWhen_CtxVarResolution(t *testing.T) {
	env := map[string]string{} // env 里没有 branch
	ctx := map[string]string{"branch": "main"}

	// 验证 ctx 变量能解析（修复前因为 env==ctx 总是命中分支错误路径）
	ok, err := EvalWhen(`branch == "main"`, env, ctx)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !ok {
		t.Error("branch from ctx should resolve to 'main'")
	}

	ok, _ = EvalWhen(`branch == "dev"`, env, ctx)
	if ok {
		t.Error("branch from ctx should NOT match 'dev'")
	}
}

func TestEvalWhen_CtxDoesNotLeakToEnv(t *testing.T) {
	// 关键回归测试：修复前 ctx 变量会和 env 混淆
	// 验证 env 里看不到 ctx-only 变量
	env := map[string]string{}
	ctx := map[string]string{"branch": "main"}

	ok, err := EvalWhen(`$branch == "main"`, env, ctx)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	// $branch 应在 env 中查不到 → 空字符串 → 不等于 "main"
	if ok {
		t.Error("$branch should NOT resolve from ctx")
	}
}

func TestEvalWhen_UnknownBareIdentifier(t *testing.T) {
	// 未知裸标识符回退到字面字符串
	ok, err := EvalWhen(`nonexistent == "nonexistent"`, nil, nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !ok {
		t.Error("unknown bare identifier should fall back to literal")
	}
}

func TestEvalWhen_OperatorPrecedence(t *testing.T) {
	// 验证 == vs >= 不冲突（>= 应该被先匹配为完整 token）
	ok, err := EvalWhen("5 >= 5", nil, nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !ok {
		t.Error("5 >= 5 should be true")
	}
}

func TestBuildGitContext_NotInGitRepo(t *testing.T) {
	// 在 /tmp 这种不可能是 git 仓库的目录运行
	ctx := BuildGitContext("/tmp")
	if len(ctx) != 0 {
		t.Errorf("expected empty ctx outside git repo, got %v", ctx)
	}
}

// TestBuildGitContext_InGitRepo 跳过除非 git 可用且 cwd 是仓库
func TestBuildGitContext_InGitRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	if runtime.GOOS == "windows" {
		t.Skip("shell-based test skipped on windows")
	}
	// 用 skating-dev repo 路径探测（已知是 git 仓库）
	ctx := BuildGitContext("/home/agentuser/projects/skating-dev/skating")
	if _, ok := ctx["branch"]; !ok {
		t.Error("branch should be set")
	}
	if _, ok := ctx["commit"]; !ok {
		t.Error("commit should be set")
	}
	if _, ok := ctx["git_dirty"]; !ok {
		t.Error("git_dirty should be set")
	}
}
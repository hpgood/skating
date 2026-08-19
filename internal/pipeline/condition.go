package pipeline

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// EvalWhen 计算条件表达式。
// 支持的操作符：==, !=, >, <, >=, <=
// $VAR 形式的变量从 env 解析，普通标识符从 ctx 解析。
// 空字符串表示无条件，始终返回 true。
func EvalWhen(condition string, env map[string]string, ctx map[string]string) (bool, error) {
	condition = strings.TrimSpace(condition)
	if condition == "" {
		return true, nil
	}

	ops := []string{">=", "<=", "!=", "==", ">", "<"}
	var op string
	var left, right string

	for _, o := range ops {
		idx := strings.Index(condition, o)
		if idx >= 0 {
			op = o
			left = strings.TrimSpace(condition[:idx])
			right = strings.TrimSpace(condition[idx+len(o):])
			break
		}
	}

	if op == "" {
		return false, fmt.Errorf("no operator found in condition: %s", condition)
	}

	leftVal := resolveVar(left, env, ctx)
	rightVal := resolveVar(right, env, ctx)

	return compare(leftVal, op, rightVal)
}

func resolveVar(s string, env, ctx map[string]string) string {
	s = strings.TrimSpace(s)
	// 去除引号
	if len(s) >= 2 && ((s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'')) {
		return s[1 : len(s)-1]
	}
	// 环境变量引用（显式 $VAR）
	if strings.HasPrefix(s, "$") {
		if v, ok := env[s[1:]]; ok {
			return v
		}
		return ""
	}
	// 裸标识符：env 优先，ctx 次之，最后回退到字面字符串
	// 这样 SKA_BUILD_ID 在 env 里能正确解析，git context 在 env 缺失时才生效
	if v, ok := env[s]; ok {
		return v
	}
	if v, ok := ctx[s]; ok {
		return v
	}
	return s
}

func compare(left, op, right string) (bool, error) {
	ln, lErr := strconv.ParseFloat(left, 64)
	rn, rErr := strconv.ParseFloat(right, 64)

	if lErr == nil && rErr == nil {
		switch op {
		case "==":
			return ln == rn, nil
		case "!=":
			return ln != rn, nil
		case ">":
			return ln > rn, nil
		case "<":
			return ln < rn, nil
		case ">=":
			return ln >= rn, nil
		case "<=":
			return ln <= rn, nil
		}
	}

	switch op {
	case "==":
		return left == right, nil
	case "!=":
		return left != right, nil
	case ">":
		return left > right, nil
	case "<":
		return left < right, nil
	case ">=":
		return left >= right, nil
	case "<=":
		return left <= right, nil
	default:
		return false, fmt.Errorf("unknown operator: %s", op)
	}
}

// BuildGitContext 自动从 git 仓库读取上下文变量
// 返回的 map 包含：branch (当前分支)、commit (短 hash)、is_dirty (布尔字符串)
// 不在 git 仓库时返回空 map（非错误）
func BuildGitContext(workDir string) map[string]string {
	ctx := map[string]string{}

	// git rev-parse --abbrev-ref HEAD
	branchCmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	if workDir != "" {
		branchCmd.Dir = workDir
	}
	if out, err := branchCmd.Output(); err == nil {
		ctx["branch"] = strings.TrimSpace(string(out))
	}

	// git rev-parse --short HEAD
	commitCmd := exec.Command("git", "rev-parse", "--short", "HEAD")
	if workDir != "" {
		commitCmd.Dir = workDir
	}
	if out, err := commitCmd.Output(); err == nil {
		ctx["commit"] = strings.TrimSpace(string(out))
	}

	// git status --porcelain (空 = clean)
	statusCmd := exec.Command("git", "status", "--porcelain")
	if workDir != "" {
		statusCmd.Dir = workDir
	}
	if out, err := statusCmd.Output(); err == nil {
		if len(strings.TrimSpace(string(out))) == 0 {
			ctx["git_dirty"] = "false"
		} else {
			ctx["git_dirty"] = "true"
		}
	}

	return ctx
}
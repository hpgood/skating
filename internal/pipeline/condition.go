package pipeline

import (
	"fmt"
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
	// 环境变量引用
	if strings.HasPrefix(s, "$") {
		if v, ok := env[s[1:]]; ok {
			return v
		}
		return ""
	}
	// 上下文变量
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
package executor

import (
	"os"
	"regexp"
	"strings"
	"sync"
)

// ANSI 前景色代码：按 shell / 大纲层级区分输出颜色
const (
	ansiReset    = "\x1b[0m"
	colorGreen   = "\x1b[32m"  // 成功状态
	colorRed     = "\x1b[31m"  // 失败状态
	colorYellow  = "\x1b[33m"  // 跳过/警告状态
	colorMagenta = "\x1b[35m"  // 次级大纲标题（--- ---）
	colorCyan    = "\x1b[36m"  // 一级大纲标题（=== ===）
	colorPS      = colorCyan   // PowerShell: 青色
	colorBash    = colorYellow // bash: 黄色
	colorDocker  = colorGreen  // Docker 容器内执行: 绿色
)

// ansiPattern 匹配 ANSI 颜色转义序列（如 \x1b[36m）
var ansiPattern = regexp.MustCompile("\x1b\\[[0-9;]*m")

// StripANSI 去除字符串中的 ANSI 颜色序列（写日志文件时保持纯文本）
func StripANSI(s string) string {
	return ansiPattern.ReplaceAllString(s, "")
}

// colorForShell 根据实际使用的 shell 选择对应颜色
func colorForShell(shell string) string {
	if isPwsh(shell) {
		return colorPS
	}
	return colorBash
}

// ColorSuccess 成功状态行用绿色
func ColorSuccess(s string) string { return colorize(colorGreen, s) }

// ColorFail 失败/错误状态行用红色
func ColorFail(s string) string { return colorize(colorRed, s) }

// ColorWarn 跳过/警告状态行用黄色
func ColorWarn(s string) string { return colorize(colorYellow, s) }

// ColorTitle 一级大纲标题（=== Build === / === 构建完成 ===）用青色
func ColorTitle(s string) string { return colorize(colorCyan, s) }

// ColorSubtitle 次级大纲标题（--- Build Summary ---）用洋红
func ColorSubtitle(s string) string { return colorize(colorMagenta, s) }

// colorize 给文本上色；颜色关闭时原样返回。
// 尾部换行不参与着色，避免 reset 码孤立成行
func colorize(color, s string) string {
	if color == "" || !colorEnabled() {
		return s
	}
	return color + strings.TrimRight(s, "\r\n") + ansiReset
}

var (
	colorOnce sync.Once
	colorOn   bool
)

// colorEnabled 决定是否输出 ANSI 颜色：
//   - SKATING_COLOR=always 强制开、=never 强制关
//   - 默认 auto：仅当 stdout 是真实终端时开（重定向/管道时保持纯文本）
func colorEnabled() bool {
	colorOnce.Do(func() {
		switch strings.ToLower(os.Getenv("SKATING_COLOR")) {
		case "always":
			colorOn = true
			return
		case "never":
			colorOn = false
			return
		}
		fi, err := os.Stdout.Stat()
		colorOn = err == nil && fi.Mode()&os.ModeCharDevice != 0
	})
	return colorOn
}

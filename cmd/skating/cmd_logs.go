package main

import (
	"fmt"
	"os"
	"sort"

	"github.com/hpgood/skating/internal/i18n"
	"github.com/hpgood/skating/internal/store"
	"github.com/spf13/cobra"
)

var logLast int
var logID int

var logsCmd = &cobra.Command{
	Use:   "logs <project-name>",
	Short: i18n.T("查看日志", "View logs"),
	Args:  cobra.ExactArgs(1),
	RunE:  runLogs,
}

func init() {
	logsCmd.Flags().IntVar(&logLast, "last", 0, i18n.T("显示最近 N 次构建日志摘要", "show summary of last N builds"))
	logsCmd.Flags().IntVar(&logID, "id", 0, i18n.T("查看指定构建ID的完整日志", "show full log for build ID N"))
}

func runLogs(cmd *cobra.Command, args []string) error {
	projName := args[0]

	s, err := store.NewStore()
	if err != nil {
		return fmt.Errorf(i18n.T("创建 store 失败: %w", "create store failed: %w"), err)
	}

	ids, err := s.ListLogs(projName)
	if err != nil {
		return fmt.Errorf(i18n.T("列出日志失败: %w", "list logs failed: %w"), err)
	}

	if len(ids) == 0 {
		fmt.Println(i18n.T("暂无构建记录", "No build records yet"))
		return nil
	}

	// 按降序排列（最新的在前）
	sort.Sort(sort.Reverse(sort.IntSlice(ids)))

	if logID > 0 {
		logContent, err := s.GetLog(projName, logID)
		if err != nil {
			fmt.Fprintf(os.Stderr, i18n.T("错误: %v\n", "Error: %v\n"), err)
			os.Exit(1)
		}
		fmt.Print(logContent)
		return nil
	}

	if logLast > 0 {
		count := logLast
		if count > len(ids) {
			count = len(ids)
		}
		for i := 0; i < count; i++ {
			buildID := ids[i]
			info, err := s.GetLog(projName, buildID)
			if err != nil {
				continue
			}
			status := "-"
			for _, line := range splitLines(info) {
				if line == "" {
					continue
				}
				status = line
				break
			}
			fmt.Printf(i18n.T("构建ID: %d  状态: %s\n", "Build ID: %d  Status: %s\n"), buildID, status)
			fmt.Println("---")
		}
		return nil
	}

	// 默认：显示最近一次构建的完整日志
	latestID := ids[0]
	logContent, err := s.GetLog(projName, latestID)
	if err != nil {
		return fmt.Errorf(i18n.T("读取日志失败: %w", "read log failed: %w"), err)
	}
	fmt.Print(logContent)
	return nil
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

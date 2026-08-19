package main

import (
	"fmt"

	"github.com/hpgood/skating/internal/i18n"
	"github.com/hpgood/skating/internal/store"
	"github.com/spf13/cobra"
)

var cleanAll bool

var cleanCmd = &cobra.Command{
	Use:   "clean <project-name>",
	Short: i18n.T("清空构建日志（保留 BuildID 和项目注册信息）", "Clear build logs (keeps BuildID and registry)"),
	Long: i18n.T(
		`清空指定项目或所有项目的历史构建日志文件。
项目注册信息和构建编号 (BuildID) 不会受影响。

示例:
  skating clean myproject   清空 myproject 的日志
  skating clean --all        清空所有项目的日志`,
		`Clear build log files for specific or all projects.
Project registry and BuildID counter are preserved.

Examples:
  skating clean myproject   Clear logs for myproject
  skating clean --all        Clear logs for all projects`,
	),
	Args: cobra.MaximumNArgs(1),
	RunE: runClean,
}

func runClean(cmd *cobra.Command, args []string) error {
	s, err := store.NewStore()
	if err != nil {
		return fmt.Errorf(i18n.T("创建 store 失败: %w", "create store failed: %w"), err)
	}

	if cleanAll {
		n, err := s.CleanAllLogs()
		if err != nil {
			return fmt.Errorf(i18n.T("清空日志失败: %w", "clear logs failed: %w"), err)
		}
		if n == 0 {
			fmt.Println(i18n.T("没有日志需要清理", "No logs to clean"))
		} else {
			fmt.Printf(i18n.T("已清空所有项目的构建日志（共 %d 个文件）\n", "Cleared all project build logs (%d files)\n"), n)
		}
		fmt.Println(i18n.T("项目注册信息和 BuildID 保持不变", "Project registry and BuildID preserved"))
		return nil
	}

	if len(args) == 0 {
		return fmt.Errorf("%s", i18n.T("请指定项目名或使用 --all 清空所有日志", "specify a project name or use --all to clean all logs"))
	}

	projName := args[0]

	_, err = s.GetProject(projName)
	if err != nil {
		return fmt.Errorf(i18n.T("项目 %q 不存在，使用 skating ls 查看可用项目", "project %q not found, use skating ls to list projects"), projName)
	}

	n, err := s.CleanLogs(projName)
	if err != nil {
		return fmt.Errorf(i18n.T("清空日志失败: %w", "clear logs failed: %w"), err)
	}
	if n == 0 {
		fmt.Printf(i18n.T("项目 %q 没有日志需要清理\n", "Project %q has no logs to clean\n"), projName)
	} else {
		fmt.Printf(i18n.T("已清空项目 %q 的构建日志（共 %d 个文件）\n", "Cleared project %q build logs (%d files)\n"), projName, n)
	}
	fmt.Printf(i18n.T("项目注册信息和 BuildID (%d) 保持不变\n", "Project registry and BuildID (%d) preserved\n"), getBuildID(s, projName))
	return nil
}

func getBuildID(s *store.Store, name string) int {
	p, err := s.GetProject(name)
	if err != nil {
		return 0
	}
	return p.BuildID
}

func init() {
	cleanCmd.Flags().BoolVar(&cleanAll, "all", false, i18n.T("清空所有项目的日志", "clear logs for all projects"))
}

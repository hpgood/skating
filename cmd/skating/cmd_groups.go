package main

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/hpgood/skating/internal/i18n"
	"github.com/hpgood/skating/internal/pipeline"
	"github.com/hpgood/skating/internal/store"
	"github.com/spf13/cobra"
)

var groupsCmd = &cobra.Command{
	Use:   "groups [project-name]",
	Short: i18n.T("列出项目支持的 group 及说明", "List project groups and descriptions"),
	Args:  cobra.MaximumNArgs(1),
	RunE:  runGroups,
}

// runGroups 列出 pipeline.groups 中的 group、stage 列表和说明。
// 无参数时直接读取当前目录的 .skating.yaml（不要求项目已注册），
// 与 run 的 ad-hoc 语义一致；有参数时从已注册项目路径读取。
func runGroups(cmd *cobra.Command, args []string) error {
	var configPath string
	if len(args) == 0 {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf(i18n.T("获取当前目录失败: %w", "get current directory failed: %w"), err)
		}
		configPath = filepath.Join(cwd, ".skating.yaml")
		if _, err := os.Stat(configPath); os.IsNotExist(err) {
			return errors.New(i18n.T("当前目录没有 .skating.yaml 配置文件，请先执行 skating init", "no .skating.yaml found in current directory, run skating init first"))
		}
	} else {
		projName := args[0]
		s, err := store.NewStore()
		if err != nil {
			return fmt.Errorf(i18n.T("创建 store 失败: %w", "create store failed: %w"), err)
		}
		project, err := s.GetProject(projName)
		if err != nil {
			return fmt.Errorf(i18n.T("项目 %q 不存在", "project %q not found"), projName)
		}
		configPath = filepath.Join(project.Path, ".skating.yaml")
	}

	cfg, err := pipeline.LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf(i18n.T("加载 pipeline 配置失败: %w", "load pipeline config failed: %w"), err)
	}

	groups := cfg.Pipeline.Groups
	if len(groups) == 0 {
		fmt.Printf(i18n.T("未定义任何 pipeline.groups: %s\n", "no pipeline.groups defined in %s\n"), configPath)
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, i18n.T("GROUP\tSTAGES\t说明", "GROUP\tSTAGES\tDESCRIPTION"))
	for _, name := range slices.Sorted(maps.Keys(groups)) {
		g := groups[name]
		desc := g.Description
		if desc == "" {
			desc = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", name, strings.Join(g.Stages, ", "), desc)
	}
	return w.Flush()
}

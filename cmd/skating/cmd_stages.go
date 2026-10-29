package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/hpgood/skating/internal/i18n"
	"github.com/hpgood/skating/internal/pipeline"
	"github.com/hpgood/skating/internal/store"
	"github.com/spf13/cobra"
)

var stagesCmd = &cobra.Command{
	Use:   "stages [project-name]",
	Short: i18n.T("列出 stage 及说明", "List stages and descriptions"),
	Args:  cobra.MaximumNArgs(1),
	RunE:  runStages,
}

// runStages 按声明顺序列出 pipeline.stages 中的 stage 及说明。
// 无参数时直接读取当前目录的 .skating.yaml（不要求项目已注册），
// 与 run 的 ad-hoc 语义一致；有参数时从已注册项目路径读取。
func runStages(cmd *cobra.Command, args []string) error {
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

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, i18n.T("STAGE\t说明", "STAGE\tDESCRIPTION"))
	for _, st := range cfg.Pipeline.Stages {
		desc := st.Description
		if desc == "" {
			desc = "-"
		}
		fmt.Fprintf(w, "%s\t%s\n", st.Name, desc)
	}
	return w.Flush()
}

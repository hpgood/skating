package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hpgood/skating/internal/i18n"
	"github.com/hpgood/skating/internal/store"
	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config [project-name]",
	Short: i18n.T("查看项目配置", "Configure project settings"),
	Args:  cobra.MaximumNArgs(1),
	RunE:  runConfig,
}

// resolveProjectName 项目名可选：显式传入优先；无参时取当前目录
// .skating.yaml 的 name（缺省用目录名）。config/logs/clean 共用。
func resolveProjectName(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	return defaultProjectName()
}

func defaultProjectName() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	name := readProjectName(filepath.Join(cwd, ".skating.yaml"))
	if name == "" {
		name = filepath.Base(cwd)
	}
	return name
}

func runConfig(cmd *cobra.Command, args []string) error {
	projName := resolveProjectName(args)

	s, err := store.NewStore()
	if err != nil {
		return fmt.Errorf(i18n.T("创建 store 失败: %w", "create store failed: %w"), err)
	}

	project, err := s.GetProject(projName)
	if err != nil {
		fmt.Fprintf(os.Stderr, i18n.T("错误: 项目 %q 不存在\n", "Error: project %q not found\n"), projName)
		os.Exit(1)
	}

	skatingYamlPath := filepath.Join(project.Path, ".skating.yaml")
	data, err := os.ReadFile(skatingYamlPath)
	if err != nil {
		return fmt.Errorf(i18n.T("读取 .skating.yaml 失败: %w", "read .skating.yaml failed: %w"), err)
	}

	fmt.Print(string(data))
	return nil
}

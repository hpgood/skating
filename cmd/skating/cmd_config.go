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
	Use:   "config <project-name>",
	Short: i18n.T("查看项目配置", "Configure project settings"),
	Args:  cobra.ExactArgs(1),
	RunE:  runConfig,
}

func runConfig(cmd *cobra.Command, args []string) error {
	projName := args[0]

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

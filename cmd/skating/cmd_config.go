package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hpgood/skating/internal/store"

	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config <项目名>",
	Short: "Configure project settings",
	Args:  cobra.ExactArgs(1),
	RunE:  runConfig,
}

func runConfig(cmd *cobra.Command, args []string) error {
	projName := args[0]

	s, err := store.NewStore()
	if err != nil {
		return fmt.Errorf("创建 store 失败: %w", err)
	}

	project, err := s.GetProject(projName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: 项目 %q 不存在\n", projName)
		os.Exit(1)
	}

	skatingYamlPath := filepath.Join(project.Path, ".skating.yaml")
	data, err := os.ReadFile(skatingYamlPath)
	if err != nil {
		return fmt.Errorf("读取 .skating.yaml 失败: %w", err)
	}

	fmt.Print(string(data))
	return nil
}

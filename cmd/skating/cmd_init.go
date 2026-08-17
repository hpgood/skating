package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/hpgood/skating/internal/i18n"
	"github.com/hpgood/skating/internal/store"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: i18n.T("初始化新项目", "Initialize a new project"),
	RunE:  runInit,
}

func runInit(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf(i18n.T("获取当前目录失败: %w", "get current directory: %w"), err)
	}
	projName := filepath.Base(cwd)

	// 1. 创建 .skating.yaml
	skatingYaml := `name: ""  # 当前目录名
image: golang:1.21
steps:
  - name: build
    type: shell
    script: |
      echo "Building with SKA_BUILD_ID=${SKA_BUILD_ID}"
      go build ./...
`
	skatingYamlPath := filepath.Join(cwd, ".skating.yaml")
	if err := os.WriteFile(skatingYamlPath, []byte(skatingYaml), 0644); err != nil {
		return fmt.Errorf(i18n.T("创建 .skating.yaml 失败: %w", "create .skating.yaml failed: %w"), err)
	}
	fmt.Println(i18n.T("已创建 ", "Created ") + skatingYamlPath)

	// 2. 创建 skating.star
	starContent := `def pipeline():
    return [
        stage(
            name = "build",
            steps = [
                step(name = "compile", type = "shell", script = "go build ./..."),
            ],
        ),
    ]
`
	starPath := filepath.Join(cwd, "skating.star")
	if err := os.WriteFile(starPath, []byte(starContent), 0644); err != nil {
		return fmt.Errorf(i18n.T("创建 skating.star 失败: %w", "create skating.star failed: %w"), err)
	}
	fmt.Println(i18n.T("已创建 ", "Created ") + starPath)

	// 3. 创建 scripts/ 目录和示例脚本
	scriptsDir := filepath.Join(cwd, "scripts")
	if err := os.MkdirAll(scriptsDir, 0755); err != nil {
		return fmt.Errorf(i18n.T("创建 scripts 目录失败: %w", "create scripts dir failed: %w"), err)
	}

	buildSh := `#!/bin/bash
echo "Building project..."
go build ./...
`
	buildShPath := filepath.Join(scriptsDir, "build.sh")
	if err := os.WriteFile(buildShPath, []byte(buildSh), 0755); err != nil {
		return fmt.Errorf(i18n.T("创建 build.sh 失败: %w", "create build.sh failed: %w"), err)
	}
	fmt.Println(i18n.T("已创建 ", "Created ") + buildShPath)

	buildLua := `-- build.lua
print("Building project...")
os.execute("go build ./...")
`
	buildLuaPath := filepath.Join(scriptsDir, "build.lua")
	if err := os.WriteFile(buildLuaPath, []byte(buildLua), 0644); err != nil {
		return fmt.Errorf(i18n.T("创建 build.lua 失败: %w", "create build.lua failed: %w"), err)
	}
	fmt.Println(i18n.T("已创建 ", "Created ") + buildLuaPath)

	// 4. 在 store 中注册项目
	s, err := store.NewStore()
	if err != nil {
		return fmt.Errorf(i18n.T("创建 store 失败: %w", "create store failed: %w"), err)
	}

	project := &store.Project{
		Name:       projName,
		Path:       cwd,
		Image:      "golang:1.21",
		BuildID:    0,
		LastStatus: "",
		LastBuild:  "",
		CreatedAt:  time.Now().Format(time.RFC3339),
	}

	if err := s.SaveProject(project); err != nil {
		return fmt.Errorf(i18n.T("保存项目失败: %w", "save project failed: %w"), err)
	}

	fmt.Printf(i18n.T("项目 %q 初始化成功。\n", "Project %q initialized successfully.\n"), projName)
	return nil
}

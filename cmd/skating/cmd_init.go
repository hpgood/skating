package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/hpgood/skating/internal/store"

	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a new project",
	RunE:  runInit,
}

func runInit(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("获取当前目录失败: %w", err)
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
		return fmt.Errorf("创建 .skating.yaml 失败: %w", err)
	}
	fmt.Printf("已创建 %s\n", skatingYamlPath)

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
		return fmt.Errorf("创建 skating.star 失败: %w", err)
	}
	fmt.Printf("已创建 %s\n", starPath)

	// 3. 创建 scripts/ 目录和示例脚本
	scriptsDir := filepath.Join(cwd, "scripts")
	if err := os.MkdirAll(scriptsDir, 0755); err != nil {
		return fmt.Errorf("创建 scripts 目录失败: %w", err)
	}

	buildSh := `#!/bin/bash
echo "Building project..."
go build ./...
`
	buildShPath := filepath.Join(scriptsDir, "build.sh")
	if err := os.WriteFile(buildShPath, []byte(buildSh), 0755); err != nil {
		return fmt.Errorf("创建 build.sh 失败: %w", err)
	}
	fmt.Printf("已创建 %s\n", buildShPath)

	buildLua := `-- build.lua
print("Building project...")
os.execute("go build ./...")
`
	buildLuaPath := filepath.Join(scriptsDir, "build.lua")
	if err := os.WriteFile(buildLuaPath, []byte(buildLua), 0644); err != nil {
		return fmt.Errorf("创建 build.lua 失败: %w", err)
	}
	fmt.Printf("已创建 %s\n", buildLuaPath)

	// 4. 在 store 中注册项目
	s, err := store.NewStore()
	if err != nil {
		return fmt.Errorf("创建 store 失败: %w", err)
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
		return fmt.Errorf("保存项目失败: %w", err)
	}

	fmt.Printf("项目 %q 初始化成功。\n", projName)
	return nil
}

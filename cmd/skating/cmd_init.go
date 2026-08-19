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

var initForce bool

var initCmd = &cobra.Command{
	Use:   "init",
	Short: i18n.T("初始化新项目", "Initialize a new project"),
	RunE:  runInit,
}

func init() {
	initCmd.Flags().BoolVar(&initForce, "force", false, i18n.T("强制覆盖已存在的配置文件", "force overwrite existing config files"))
}

func runInit(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf(i18n.T("获取当前目录失败: %w", "get current directory: %w"), err)
	}
	projName := filepath.Base(cwd)

	// 1. 创建 .skating.yaml（schema 必须与 internal/pipeline/dsl.go 一致：pipeline.stages[].steps[]）
	skatingYaml := fmt.Sprintf(`name: %q
image: golang:1.21
pipeline:
  stages:
    - name: build
      parallel: false
      steps:
        - name: compile
          type: shell
          script: |
            echo "Building with SKA_BUILD_ID=${SKA_BUILD_ID}"
            go build ./...
`, projName)
	skatingYamlPath := filepath.Join(cwd, ".skating.yaml")
	if err := writeIfAllowed(skatingYamlPath, []byte(skatingYaml), 0644, initForce); err != nil {
		return fmt.Errorf(i18n.T("创建 .skating.yaml 失败: %w", "create .skating.yaml failed: %w"), err)
	}
	fmt.Println(i18n.T("已创建 ", "Created ") + skatingYamlPath)

	// 2. 创建 scripts/ 目录和示例脚本
	scriptsDir := filepath.Join(cwd, "scripts")
	if err := os.MkdirAll(scriptsDir, 0755); err != nil {
		return fmt.Errorf(i18n.T("创建 scripts 目录失败: %w", "create scripts dir failed: %w"), err)
	}

	buildSh := `#!/bin/bash
echo "Building project..."
go build ./...
`
	buildShPath := filepath.Join(scriptsDir, "build.sh")
	if err := writeIfAllowed(buildShPath, []byte(buildSh), 0755, initForce); err != nil {
		return fmt.Errorf(i18n.T("创建 build.sh 失败: %w", "create build.sh failed: %w"), err)
	}
	fmt.Println(i18n.T("已创建 ", "Created ") + buildShPath)

	// Lua 示例：用安全的 sh() API（沙箱已禁 os.execute）
	buildLua := `-- build.lua
print("Building project...")
local out, err = sh("go build ./...")
if err then
  error("build failed: " .. err)
end
print(out)
`
	buildLuaPath := filepath.Join(scriptsDir, "build.lua")
	if err := writeIfAllowed(buildLuaPath, []byte(buildLua), 0644, initForce); err != nil {
		return fmt.Errorf(i18n.T("创建 build.lua 失败: %w", "create build.lua failed: %w"), err)
	}
	fmt.Println(i18n.T("已创建 ", "Created ") + buildLuaPath)

	// 3. 在 store 中注册项目
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

// writeIfAllowed 在 force 为 true 或文件不存在时写入，否则返回 ErrFileExists
func writeIfAllowed(path string, data []byte, perm os.FileMode, force bool) error {
	if !force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("file already exists (use --force to overwrite): %s", path)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return os.WriteFile(path, data, perm)
}
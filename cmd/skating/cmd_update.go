package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/hpgood/skating/internal/i18n"
	"github.com/hpgood/skating/internal/store"
	"github.com/spf13/cobra"
)

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: i18n.T("将当前目录 .skating.yaml 的 name 同步到全局项目注册", "Sync project name from local .skating.yaml to the global project registry"),
	RunE:  runUpdate,
}

func runUpdate(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf(i18n.T("获取当前目录失败: %w", "get current directory failed: %w"), err)
	}

	configPath := filepath.Join(cwd, ".skating.yaml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return errors.New(i18n.T("当前目录没有 .skating.yaml 配置文件，请先执行 skating init", "no .skating.yaml found in current directory, run skating init first"))
	}

	// 从 .skating.yaml 读取 name，缺省时用目录名
	name := readProjectName(configPath)
	if name == "" {
		name = filepath.Base(cwd)
	}

	s, err := store.NewStore()
	if err != nil {
		return fmt.Errorf(i18n.T("创建 store 失败: %w", "create store failed: %w"), err)
	}

	projects, err := s.ListProjects()
	if err != nil {
		return fmt.Errorf(i18n.T("读取项目列表失败: %w", "list projects failed: %w"), err)
	}

	cwdClean := filepath.Clean(cwd)
	var byName, byPath *store.Project
	for i := range projects {
		if projects[i].Name == name && byName == nil {
			byName = &projects[i]
		}
		if filepath.Clean(projects[i].Path) == cwdClean && byPath == nil {
			byPath = &projects[i]
		}
	}

	switch {
	case byName != nil:
		// 名字已注册：同步 path
		byName.Path = cwd
		if err := s.SaveProject(byName); err != nil {
			return fmt.Errorf(i18n.T("保存项目失败: %w", "save project failed: %w"), err)
		}
		fmt.Printf(i18n.T("项目 %q 已更新 (path: %s)\n", "Project %q updated (path: %s)\n"), name, cwd)

	case byPath != nil:
		// 同路径旧记录存在但名字不匹配 → yaml 里的 name 改过了，执行重命名
		oldName := byPath.Name
		byPath.Name = name
		byPath.Path = cwd
		if err := s.RemoveProject(oldName); err != nil {
			return fmt.Errorf(i18n.T("移除旧项目记录失败: %w", "remove old project record failed: %w"), err)
		}
		if err := s.SaveProject(byPath); err != nil {
			return fmt.Errorf(i18n.T("保存项目失败: %w", "save project failed: %w"), err)
		}
		fmt.Printf(i18n.T("项目已重命名 %q → %q\n", "Project renamed %q → %q\n"), oldName, name)

	default:
		// 未注册 → 注册新项目
		project := &store.Project{
			Name:      name,
			Path:      cwd,
			CreatedAt: time.Now().Format(time.RFC3339),
		}
		if err := s.SaveProject(project); err != nil {
			return fmt.Errorf(i18n.T("保存项目失败: %w", "save project failed: %w"), err)
		}
		fmt.Printf(i18n.T("项目 %q 已注册 (path: %s)\n", "Project %q registered (path: %s)\n"), name, cwd)
	}

	return nil
}

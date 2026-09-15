package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hpgood/skating/internal/i18n"
	"github.com/hpgood/skating/internal/store"
	"github.com/spf13/cobra"
)

var configGlobal bool // 查看/操作全局配置而非项目配置

var configCmd = &cobra.Command{
	Use:   "config [project-name]",
	Short: i18n.T("查看项目配置（--global 查看全局配置）", "Show project config (--global for global config)"),
	Args:  cobra.MaximumNArgs(1),
	RunE:  runConfig,
}

func init() {
	configCmd.Flags().BoolVarP(&configGlobal, "global", "g", false,
		i18n.T("查看全局配置（~/.skating/config.yaml）", "show global config (~/.skating/config.yaml)"))
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
	s, err := store.NewStore()
	if err != nil {
		return fmt.Errorf(i18n.T("创建 store 失败: %w", "create store failed: %w"), err)
	}

	if configGlobal {
		return showGlobalConfig(s)
	}

	projName := resolveProjectName(args)

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

// showGlobalConfig 展示全局配置（~/.skating/config.yaml），缺省键标注默认值来源
func showGlobalConfig(s *store.Store) error {
	gc, err := s.GetGlobalConfig()
	if err != nil {
		return fmt.Errorf(i18n.T("读取全局配置失败: %w", "load global config failed: %w"), err)
	}

	path := s.GlobalConfigPath()
	fmt.Println(i18n.T("全局配置文件: ", "Global config file: ") + path)

	// 文件里没有显式写 max_tasks 时为默认值
	explicit := false
	if data, err := os.ReadFile(path); err == nil && strings.Contains(string(data), "max_tasks") {
		explicit = true
	}
	fmt.Printf("max_tasks: %d", gc.MaxTasks)
	if !explicit {
		fmt.Print(i18n.T("  (默认值 = CPU 核数)", "  (default = number of CPUs)"))
	}
	fmt.Println()

	fmt.Println(i18n.T("\n修改方式：编辑上述文件，例如：",
		"\nTo change: edit the file above, e.g.:"))
	fmt.Println("  max_tasks: 4")
	return nil
}

package main

import (
	"fmt"
	"os"

	"github.com/hpgood/skating/internal/plugin"
	"github.com/spf13/cobra"
)

var version = "0.1.0"

func main() {
	// 启动时加载用户插件目录（目录不存在则跳过）
	if err := plugin.LoadPlugins("plugins"); err != nil {
		fmt.Fprintf(os.Stderr, "加载插件失败: %v\n", err)
	}

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:     "skating",
	Short:   "skating is a CLI tool for project management",
	Version: version,
	Run: func(cmd *cobra.Command, args []string) {
		cmd.Help()
	},
}

func init() {
	rootCmd.AddCommand(initCmd, configCmd, lsCmd, runCmd, logsCmd)
}

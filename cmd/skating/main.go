package main

import (
	"fmt"
	"os"

	"github.com/hpgood/skating/internal/i18n"
	"github.com/hpgood/skating/internal/plugin"
	"github.com/spf13/cobra"
)

var (
	version = "dev"     // injected via -ldflags "-X main.version=1.0.0"
	commit  = "none"    // injected via -ldflags "-X main.commit=abc123"
	date    = "unknown" // injected via -ldflags "-X main.date=2024-01-01"
	lang    string
)

func main() {
	if err := plugin.LoadPlugins("plugins"); err != nil {
		fmt.Fprintf(os.Stderr, "load plugins: %v\n", err)
	}

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:     "skating",
	Short:   "skating - lightweight CI/CD build tool (no database required)",
	Version: fmt.Sprintf("%s (commit: %s, built: %s)", version, commit, date),
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		i18n.SetLang(lang)
	},
	Run: func(cmd *cobra.Command, args []string) {
		cmd.Help()
	},
}

func init() {
	rootCmd.PersistentFlags().StringVar(&lang, "lang", "en", "language (en, zh-CN)")
	rootCmd.AddCommand(initCmd, configCmd, lsCmd, runCmd, logsCmd, cleanCmd, skillCmd, versionCmd)
}

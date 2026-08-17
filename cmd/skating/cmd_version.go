package main

import (
	"fmt"

	"github.com/hpgood/skating/internal/i18n"
	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: i18n.T("输出版本号", "Print version"),
	Run:   runVersion,
}

func runVersion(cmd *cobra.Command, args []string) {
	fmt.Printf("skating %s\n", version)
	fmt.Printf("  commit : %s\n", commit)
	fmt.Printf("  built  : %s\n", date)
}
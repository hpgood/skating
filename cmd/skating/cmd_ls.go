package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/hpgood/skating/internal/i18n"
	"github.com/hpgood/skating/internal/store"
	"github.com/spf13/cobra"
)

var lsCmd = &cobra.Command{
	Use:   "ls",
	Short: i18n.T("列出项目", "List projects"),
	RunE:  runLs,
}

func runLs(cmd *cobra.Command, args []string) error {
	s, err := store.NewStore()
	if err != nil {
		return fmt.Errorf(i18n.T("创建 store 失败: %w", "create store failed: %w"), err)
	}

	projects, err := s.ListProjects()
	if err != nil {
		return fmt.Errorf(i18n.T("列出项目失败: %w", "list projects failed: %w"), err)
	}

	if len(projects) == 0 {
		fmt.Println(i18n.T("暂无项目，使用 skating init 创建", "No projects yet, use skating init to create one"))
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, i18n.T("项目名\t路径\t镜像\t构建状态\t上次构建", "NAME\tPATH\tIMAGE\tSTATUS\tLAST BUILD"))
	for _, p := range projects {
		status := p.LastStatus
		if status == "" {
			status = "-"
		}
		lastBuild := p.LastBuild
		if lastBuild == "" {
			lastBuild = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", p.Name, p.Path, p.Image, status, lastBuild)
	}
	w.Flush()
	return nil
}

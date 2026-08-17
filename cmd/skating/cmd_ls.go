package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/hpgood/skating/internal/store"

	"github.com/spf13/cobra"
)

var lsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List projects",
	RunE:  runLs,
}

func runLs(cmd *cobra.Command, args []string) error {
	s, err := store.NewStore()
	if err != nil {
		return fmt.Errorf("创建 store 失败: %w", err)
	}

	projects, err := s.ListProjects()
	if err != nil {
		return fmt.Errorf("列出项目失败: %w", err)
	}

	if len(projects) == 0 {
		fmt.Println("暂无项目，使用 skating init 创建")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "项目名\t路径\t镜像\t构建状态\t上次构建")
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

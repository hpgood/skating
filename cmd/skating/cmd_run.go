package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hpgood/skating/internal/executor"
	"github.com/hpgood/skating/internal/pipeline"
	"github.com/hpgood/skating/internal/plugin"
	"github.com/hpgood/skating/internal/store"
	"github.com/spf13/cobra"
)

var runCmd = &cobra.Command{
	Use:   "run <项目名>",
	Short: "Run a build",
	Args:  cobra.ExactArgs(1),
	RunE:  runRun,
}

func runRun(cmd *cobra.Command, args []string) error {
	projName := args[0]

	// 1. 获取项目信息
	s, err := store.NewStore()
	if err != nil {
		return fmt.Errorf("创建 store 失败: %w", err)
	}

	project, err := s.GetProject(projName)
	if err != nil {
		return fmt.Errorf("项目 %q 不存在", projName)
	}

	// 2. 读取 .skating.yaml
	configPath := filepath.Join(project.Path, ".skating.yaml")
	pl, err := pipeline.LoadPipeline(configPath)
	if err != nil {
		return fmt.Errorf("加载 pipeline 配置失败: %w", err)
	}

	// 读取镜像配置
	image := readImage(configPath)

	// 3. 获取构建编号
	buildID, err := s.NextBuildID(projName)
	if err != nil {
		return fmt.Errorf("获取构建编号失败: %w", err)
	}

	startTime := time.Now()

	// 打印构建头部
	fmt.Printf("=== 构建项目: %s (Build #%d) ===\n", projName, buildID)
	if image != "" {
		fmt.Printf("Docker 镜像: %s\n", image)
	}
	fmt.Println()

	// 4. 准备环境变量
	env := map[string]string{
		"SKA_BUILD_ID": fmt.Sprintf("%d", buildID),
	}

	// 日志收集：同时输出到终端和缓冲区
	var logBuf bytes.Buffer
	logFn := func(msg string) {
		fmt.Println(msg)
		logBuf.WriteString(msg)
		logBuf.WriteString("\n")
	}

	// 创建适配器：将 executor.Runner 适配到 pipeline.Executor 接口
	runner := executor.NewRunner()
	defer runner.CloseLua()

	adapter := &executorAdapter{
		runner:  runner,
		workDir: project.Path,
	}

	// 5. 执行流水线
	results, runErr := pipeline.RunPipeline(pl, adapter, env, logFn)

	elapsed := time.Since(startTime)

	// 6. 确定整体状态
	fmt.Println()
	overallStatus := "success"
	if runErr != nil {
		overallStatus = "failure"
		fmt.Printf("=== 构建失败: %v ===\n", runErr)
	} else {
		fmt.Println("=== 构建完成: success ===")
	}

	// 7. 保存日志
	if err := s.SaveLog(projName, buildID, &logBuf); err != nil {
		fmt.Fprintf(os.Stderr, "警告: 保存日志失败: %v\n", err)
	} else {
		home, _ := os.UserHomeDir()
		logFilePath := filepath.Join(home, ".skating", "logs", projName, fmt.Sprintf("%d.log", buildID))
		fmt.Printf("日志已保存: %s\n", logFilePath)
	}

	// 8. 更新项目状态
	project.LastStatus = overallStatus
	project.LastBuild = startTime.Format(time.RFC3339)
	if err := s.SaveProject(project); err != nil {
		fmt.Fprintf(os.Stderr, "警告: 更新项目状态失败: %v\n", err)
	}

	// 9. 打印构建摘要
	printSummary(results, elapsed)

	// 10. 执行所有已注册插件
	plugin.RunPlugins(&plugin.Context{
		ProjectName: projName,
		BuildID:     buildID,
		Status:      overallStatus,
		Duration:    elapsed.Truncate(time.Millisecond).String(),
		Output:      logBuf.String(),
	})

	if runErr != nil {
		return runErr
	}
	return nil
}

// readImage 从 .skating.yaml 中解析 image 字段
func readImage(configPath string) string {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "image:") {
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "image:"))
			val = strings.Trim(val, `"'`)
			return val
		}
	}
	return ""
}

// executorAdapter 将 executor.Runner 适配到 pipeline.Executor 接口
type executorAdapter struct {
	runner  *executor.Runner
	workDir string
}

func (a *executorAdapter) Execute(step pipeline.Step, env map[string]string) (string, error) {
	es := executor.Step{
		Name:   step.Name,
		Type:   step.Type,
		Script: step.Script,
		Source: step.Source,
	}
	return a.runner.Execute(es, a.workDir, env)
}

// printSummary 打印构建摘要
func printSummary(results []*pipeline.PipelineResult, elapsed time.Duration) {
	if len(results) == 0 {
		return
	}

	fmt.Println("--- 构建摘要 ---")
	for _, r := range results {
		icon := "✓"
		if r.Status == "failed" {
			icon = "✗"
		} else if r.Status == "skipped" {
			icon = "○"
		}
		fmt.Printf("  [%s/%s] %s %s (%s)\n", r.StageName, r.StepName, icon, r.Status, r.Duration)
	}
	fmt.Printf("\n总耗时: %s\n", elapsed.Truncate(time.Millisecond))
}

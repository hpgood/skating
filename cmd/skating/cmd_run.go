package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hpgood/skating/internal/executor"
	"github.com/hpgood/skating/internal/i18n"
	"github.com/hpgood/skating/internal/pipeline"
	"github.com/hpgood/skating/internal/plugin"
	"github.com/hpgood/skating/internal/store"
	"github.com/spf13/cobra"
)

var (
	// runStep 指定只运行某个 stage 或 step。
	//   "build"             → 只运行 stage=build 的所有 step
	//   "build/compile"     → 只运行 stage=build 里的 step=compile
	runStep string

	// runUserEnv 是用户通过 -e KEY=VAL 传入的环境变量。
	// StringArray：允许重复 -e 传多个；后传入的同名变量覆盖前面的。
	// 最终注入到 pipeline env 时优先级高于 SKA_ 系统 env（用户显式传值胜出）。
	runUserEnv []string
)

var runCmd = &cobra.Command{
	Use:   "run <project-name>",
	Short: i18n.T("运行构建", "Run a build"),
	Args:  cobra.ExactArgs(1),
	RunE:  runRun,
}

func init() {
	runCmd.Flags().StringVar(&runStep, "step", "", i18n.T("只运行指定的 stage 或 step（格式: <stage> 或 <stage>/<step>）", "run only the named stage/step (format: <stage> or <stage>/<step>)"))
	runCmd.Flags().StringArrayVar(&runUserEnv, "env", nil, i18n.T("向 step 注入自定义环境变量，格式 KEY=VAL，可重复传入", "inject custom env into steps, format KEY=VAL, repeatable"))
}

func runRun(cmd *cobra.Command, args []string) error {
	projName := args[0]

	s, err := store.NewStore()
	if err != nil {
		return fmt.Errorf(i18n.T("创建 store 失败: %w", "create store failed: %w"), err)
	}

	project, err := s.GetProject(projName)
	if err != nil {
		return fmt.Errorf(i18n.T("项目 %q 不存在", "project %q not found"), projName)
	}

	configPath := filepath.Join(project.Path, ".skating.yaml")
	pl, err := pipeline.LoadPipeline(configPath)
	if err != nil {
		return fmt.Errorf(i18n.T("加载 pipeline 配置失败: %w", "load pipeline config failed: %w"), err)
	}

	image := readImage(configPath)

	buildID, err := s.NextBuildID(projName)
	if err != nil {
		return fmt.Errorf(i18n.T("获取构建编号失败: %w", "get build ID failed: %w"), err)
	}
	// NextBuildID 已将 BuildID 持久化到磁盘，同步本地 project 副本避免后续 SaveProject 覆盖回旧值
	project.BuildID = buildID
	// 同步 image 字段到 store，保证 `skating ls` 显示最新镜像
	project.Image = image

	startTime := time.Now()

	fmt.Printf(i18n.T("=== 构建项目: %s (Build #%d) ===\n", "=== Build: %s (Build #%d) ===\n"), projName, buildID)
	if image != "" {
		fmt.Printf(i18n.T("Docker 镜像: %s\n", "Docker image: %s\n"), image)
	}
	fmt.Println()

	// 4. 准备环境变量
	// SKA_BUILD_TIMESTAMP: 整数 unix 秒 (UTC, 自 epoch 起). 用于排序/diff/算耗时
	// SKA_BUILD_DATE:      RFC3339 字符串 (UTC), 人类可读日期
	// 用户脚本需要本地时区可基于 SKA_BUILD_TIMESTAMP 自行换算
	env := map[string]string{
		"SKA_BUILD_ID":        fmt.Sprintf("%d", buildID),
		"SKA_BUILD_TIMESTAMP": fmt.Sprintf("%d", startTime.Unix()),
		"SKA_BUILD_DATE":      startTime.UTC().Format(time.RFC3339),
	}

	// 解析 --step 过滤参数。先验证 stage/step 都存在，再交给 runner。
	runOpts, err := parseRunStep(runStep, pl)
	if err != nil {
		return err
	}

	// 解析 -e KEY=VAL 注入。重复传入时后者覆盖前者；冲突时 user env 覆盖 SKA_ 系统 env。
	if err := injectUserEnv(env, runUserEnv); err != nil {
		return err
	}

	// 日志收集：同时输出到终端和缓冲区
	var logBuf bytes.Buffer
	logFn := func(msg string) {
		fmt.Println(msg)
		logBuf.WriteString(msg)
		logBuf.WriteString("\n")
	}

	// 创建适配器：将 executor.Runner 适配到 pipeline.Executor 接口
	runner := executor.NewRunnerWithImage(image)
	defer runner.CloseLua()

	if image != "" && !runner.IsDockerAvailable() {
		fmt.Fprintf(os.Stderr, i18n.T("警告: 配置了 Docker 镜像 %q 但 host 未检测到 docker 命令，将使用 host shell 执行。\n",
			"Warning: Docker image %q configured but `docker` CLI not found, falling back to host shell.\n"), image)
	}

	adapter := &executorAdapter{
		runner:  runner,
		workDir: project.Path,
	}

	// 5. 执行流水线
	results, runErr := pipeline.RunPipeline(pl, adapter, env, logFn, runOpts)

	elapsed := time.Since(startTime)

	// 6. 确定整体状态
	fmt.Println()
	overallStatus := "success"
	if runErr != nil {
		overallStatus = "failure"
		fmt.Printf(i18n.T("=== 构建失败: %v ===\n", "=== Build FAILED: %v ===\n"), runErr)
	} else {
		fmt.Println(i18n.T("=== 构建完成: success ===", "=== Build SUCCESS ==="))
	}

	// 7. 保存日志
	if err := s.SaveLog(projName, buildID, &logBuf); err != nil {
		fmt.Fprintf(os.Stderr, i18n.T("警告: 保存日志失败: %v\n", "Warning: save log failed: %v\n"), err)
	} else {
		home, _ := os.UserHomeDir()
		logFilePath := filepath.Join(home, ".skating", "logs", projName, fmt.Sprintf("%d.log", buildID))
		fmt.Printf(i18n.T("日志已保存: %s\n", "Log saved: %s\n"), logFilePath)
	}

	// 8. 更新项目状态
	project.LastStatus = overallStatus
	project.LastBuild = startTime.Format(time.RFC3339)
	if err := s.SaveProject(project); err != nil {
		fmt.Fprintf(os.Stderr, i18n.T("警告: 更新项目状态失败: %v\n", "Warning: update project status failed: %v\n"), err)
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

	fmt.Println(i18n.T("--- 构建摘要 ---", "--- Build Summary ---"))
	for _, r := range results {
		icon := "✓"
		if r.Status == "failed" {
			icon = "✗"
		} else if r.Status == "skipped" {
			icon = "○"
		}
		fmt.Printf("  [%s/%s] %s %s (%s)\n", r.StageName, r.StepName, icon, r.Status, r.Duration)
	}
	fmt.Printf(i18n.T("\n总耗时: %s\n", "\nTotal time: %s\n"), elapsed.Truncate(time.Millisecond))
}

// parseRunStep 把 --step "X" 或 "X/Y" 解析成 RunOptions。
//
// 不允许的格式:
//   - 空字符串 → 返回 nil（runner 走默认全跑）
//   - "X" 但 X 不在任何 stage 的名字里 → 报错并打印已知的 stage 列表
//   - "X/Y" 但 X 不存在 / Y 不在 X stage 里 → 报错并打印 stage X 的 step 列表
//
// 字符串里允许出现 '/' 但仅切第一刀，避免 "stage/name/extra" 误解析。
func parseRunStep(raw string, pl *pipeline.Pipeline) (*pipeline.RunOptions, error) {
	if raw == "" {
		return nil, nil
	}

	stageName, stepName, hasStep := strings.Cut(raw, "/")

	var foundStage bool
	for _, s := range pl.Stages {
		if s.Name == stageName {
			foundStage = true
			if !hasStep {
				return &pipeline.RunOptions{StageName: stageName}, nil
			}
			// 验证 step 在该 stage 里
			for _, st := range s.Steps {
				if st.Name == stepName {
					return &pipeline.RunOptions{StageName: stageName, StepName: stepName}, nil
				}
			}
			// stage 有但 step 不对 → 列已知 step 帮助调试
			var known []string
			for _, st := range s.Steps {
				known = append(known, st.Name)
			}
			return nil, fmt.Errorf(i18n.T(
				"--step %q 中 step %q 不存在于 stage %q 中。已知 step: %v",
				"step %q in --step %q not found in stage %q. Known steps: %v"),
				raw, stepName, stageName, known)
		}
	}

	if !foundStage {
		var known []string
		for _, s := range pl.Stages {
			known = append(known, s.Name)
		}
		return nil, fmt.Errorf(i18n.T(
			"--step %q 中 stage %q 不存在。已知 stage: %v",
			"stage %q in --step %q not found. Known stages: %v"),
			raw, stageName, known)
	}

	// 不可达，但编译器要求
	return nil, nil
}

// injectUserEnv 把 -e KEY=VAL 列表注入到 env map。
//
// 规则:
//   - 每条必须是 KEY=VAL 格式（必须含一个且仅一个 '='）
//   - KEY 不能为空
//   - 同名重复传：后者覆盖前者
//   - 与已有 env 冲突：用户值覆盖（语义：用户显式 > skating 系统默认）
func injectUserEnv(env map[string]string, pairs []string) error {
	for _, kv := range pairs {
		// 只切第一个 '='，允许 VAL 里含 '='
		idx := strings.Index(kv, "=")
		if idx <= 0 {
			return fmt.Errorf(i18n.T(
				"--env %q 格式错误（应为 KEY=VAL）",
				"--env %q malformed (want KEY=VAL)"),
				kv)
		}
		key := kv[:idx]
		val := kv[idx+1:]
		if key == "" {
			return fmt.Errorf(i18n.T(
				"--env %q KEY 不能为空",
				"--env %q has empty KEY"),
				kv)
		}
		env[key] = val
	}
	return nil
}

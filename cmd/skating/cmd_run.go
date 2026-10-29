package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/hpgood/skating/internal/executor"
	"github.com/hpgood/skating/internal/i18n"
	"github.com/hpgood/skating/internal/pipeline"
	"github.com/hpgood/skating/internal/plugin"
	"github.com/hpgood/skating/internal/runstate"
	"github.com/hpgood/skating/internal/store"
	"github.com/spf13/cobra"
)

var (
	// runStep 指定只运行某个 stage 或 step。
	//   "build"             → 只运行 stage=build 的所有 step
	//   "build/compile"     → 只运行 stage=build 里的 step=compile
	runStep string

	// runGroup 指定只运行 .skating.yaml 中 pipeline.groups 定义的某个 stage 组。
	// 与 runStep 互斥。
	runGroup string

	// runUserEnv 是用户通过 -e KEY=VAL 传入的环境变量。
	// StringArray：允许重复 -e 传多个；后传入的同名变量覆盖前面的。
	// 最终注入到 pipeline env 时优先级高于 SKA_ 系统 env（用户显式传值胜出）。
	runUserEnv []string

	// runTimeout 通过 --timeout 覆盖 .skating.yaml 的运行超时。
	// 接受 Go duration（"10m"/"600s"）或纯数字（按秒）。空串表示不覆盖。
	runTimeout string
)

var runCmd = &cobra.Command{
	Use:   "run [project-name[+group]]",
	Short: i18n.T("运行构建", "Run a build"),
	Args:  cobra.MaximumNArgs(1),
	RunE:  runRun,
}

func init() {
	runCmd.Flags().StringVarP(&runStep, "step", "s", "", i18n.T("只运行指定的 stage 或 step（格式: <stage> 或 <stage>/<step>）", "run only the named stage/step (format: <stage> or <stage>/<step>)"))
	runCmd.Flags().StringVarP(&runGroup, "group", "g", "", i18n.T("只运行指定 group 内的 stage（在 .skating.yaml 的 pipeline.groups 中定义）", "run only the stages in the named group (defined under pipeline.groups)"))
	runCmd.Flags().StringArrayVarP(&runUserEnv, "env", "e", nil, i18n.T("向 step 注入自定义环境变量，格式 KEY=VAL，可重复传入", "inject custom env into steps, format KEY=VAL, repeatable"))
	runCmd.Flags().StringVar(&runTimeout, "timeout", "", i18n.T("覆盖本次运行的超时时间（格式: 10m / 600s / 600）", "override the run timeout (format: 10m / 600s / 600)"))
}

func runRun(cmd *cobra.Command, args []string) error {
	// 位置参数支持 <project>+<group> / +<group> 简写（等价于 --group）。
	// 无 '+' 时保持旧行为：args[0] 即项目名。
	var rawArg string
	if len(args) > 0 {
		rawArg = args[0]
	}
	argProj, argGroup, hasSep := splitGroupArg(rawArg)

	// 位置 +group 与 -g/--group 互斥
	if hasSep && runGroup != "" {
		return errors.New(i18n.T(
			"位置参数 +<group> 与 --group 不能同时使用",
			"group specified twice: positional +<group> and --group"))
	}
	// 空 group 名（"prj+" / "+"）明确报错，不静默当作纯项目名
	if hasSep && argGroup == "" {
		return fmt.Errorf(i18n.T(
			"位置参数 %q 中 + 后缺少 group 名",
			"missing group name after '+' in %q"), rawArg)
	}

	groupName := runGroup
	if hasSep {
		groupName = argGroup
	}
	// group 与 step 互斥（含位置 +group 形式）。校验前移到 store/NextBuildID 之前，
	// 避免无效命令也递增 build 号、打印构建标题。
	if runStep != "" && groupName != "" {
		return errors.New(i18n.T("--group 与 --step 不能同时使用", "--group and --step are mutually exclusive"))
	}

	// --timeout 覆盖 .skating.yaml 的 timeout。同样前移校验，非法值不产生副作用。
	var cliTimeout time.Duration
	if runTimeout != "" {
		d, err := pipeline.ParseDuration(runTimeout)
		if err != nil {
			return fmt.Errorf(i18n.T(
				"--timeout %q 无效（示例: 10m / 600s / 600）: %w",
				"invalid --timeout %q (examples: 10m / 600s / 600): %w"), runTimeout, err)
		}
		if d <= 0 {
			return fmt.Errorf(i18n.T("--timeout 必须为正数，得到 %q", "--timeout must be positive, got %q"), runTimeout)
		}
		cliTimeout = d
	}

	var projName string
	var projPath string
	// isAdHoc：无位置参数，或位置参数为 +group（无项目名）。
	// 注意显式空串 `skating run ""` 仍按“项目名为空”处理（保持旧行为），不算 ad-hoc。
	isAdHoc := len(args) == 0 || (hasSep && argProj == "")

	if isAdHoc {
		var err error
		projPath, err = os.Getwd()
		if err != nil {
			return fmt.Errorf(i18n.T("获取当前目录失败: %w", "get current directory failed: %w"), err)
		}
		configPath := filepath.Join(projPath, ".skating.yaml")
		if _, err := os.Stat(configPath); os.IsNotExist(err) {
			return errors.New(i18n.T("当前目录没有 .skating.yaml 配置文件，请先执行 skating init", "no .skating.yaml found in current directory, run skating init first"))
		}
		projName = readProjectName(configPath)
		if projName == "" {
			projName = filepath.Base(projPath)
		}
	} else {
		projName = argProj
	}

	s, err := store.NewStore()
	if err != nil {
		return fmt.Errorf(i18n.T("创建 store 失败: %w", "create store failed: %w"), err)
	}

	project, err := s.GetProject(projName)
	if err != nil {
		if isAdHoc {
			project = &store.Project{
				Name:      projName,
				Path:      projPath,
				CreatedAt: time.Now().Format(time.RFC3339),
			}
			if err := s.SaveProject(project); err != nil {
				return fmt.Errorf(i18n.T("自动注册项目失败: %w", "auto-register project failed: %w"), err)
			}
		} else {
			return fmt.Errorf(i18n.T("项目 %q 不存在", "project %q not found"), projName)
		}
	} else if isAdHoc {
		project.Path = projPath
	}

	configPath := filepath.Join(project.Path, ".skating.yaml")
	cfg, err := pipeline.LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf(i18n.T("加载 pipeline 配置失败: %w", "load pipeline config failed: %w"), err)
	}
	pl := &pipeline.Pipeline{Stages: cfg.Pipeline.Stages}

	buildID, err := s.NextBuildID(projName)
	if err != nil {
		return fmt.Errorf(i18n.T("获取构建编号失败: %w", "get build ID failed: %w"), err)
	}
	// NextBuildID 已将 BuildID 持久化到磁盘，同步本地 project 副本避免后续 SaveProject 覆盖回旧值
	project.BuildID = buildID

	startTime := time.Now()

	fmt.Printf(i18n.T("skating 版本: %s\n", "skating version: %s\n"), version)
	fmt.Println(executor.ColorTitle(fmt.Sprintf(i18n.T("=== 构建项目: %s (Build #%d) ===", "=== Build: %s (Build #%d) ==="), projName, buildID)))
	fmt.Println()

	// 4. 准备环境变量
	// SKA_BUILD_TIMESTAMP: 整数 unix 秒 (UTC, 自 epoch 起). 用于排序/diff/算耗时
	// SKA_BUILD_DATE:      RFC3339 字符串 (UTC), 人类可读日期
	// 用户脚本需要本地时区可基于 SKA_BUILD_TIMESTAMP 自行换算
	// SKA_GIT_COMMIT_ID:   项目当前 git commit id (完整 hash); 项目无 .git 时为空
	// SKA_OS:              运行时操作系统类型 (runtime.GOOS), 如 linux / windows / darwin
	env := map[string]string{
		"SKA_BUILD_ID":        fmt.Sprintf("%d", buildID),
		"SKA_BUILD_TIMESTAMP": fmt.Sprintf("%d", startTime.Unix()),
		"SKA_BUILD_DATE":      startTime.UTC().Format(time.RFC3339),
		"SKA_GIT_COMMIT_ID":   gitCommitID(project.Path),
		"SKA_OS":              runtime.GOOS,
	}

	// 解析 --step / --group 过滤参数（--group 与 --step 已在函数开头校验互斥）。
	// 先验证 stage/step/group 都存在，再交给 runner。
	var runOpts *pipeline.RunOptions
	if groupName != "" {
		runOpts, err = parseRunGroup(groupName, cfg)
	} else {
		runOpts, err = parseRunStep(runStep, pl)
	}
	if err != nil {
		return err
	}

	// 解析 -e KEY=VAL 注入。重复传入时后者覆盖前者；冲突时 user env 覆盖 SKA_ 系统 env。
	if err := injectUserEnv(env, runUserEnv); err != nil {
		return err
	}

	// ---- 运行超时：--timeout 优先，其次 .skating.yaml 的 timeout 字段，缺省 10m ----
	timeout := cfg.Timeout.Or(pipeline.DefaultTimeout)
	if cliTimeout > 0 {
		timeout = cliTimeout
	}

	// ---- 并发闸门 + 运行状态记录（bbolt）----
	gc, err := s.GetGlobalConfig()
	if err != nil {
		return fmt.Errorf(i18n.T("读取全局配置失败: %w", "load global config failed: %w"), err)
	}
	tracker, err := runstate.Open(projName, buildID)
	if err != nil {
		return fmt.Errorf(i18n.T("创建运行状态追踪失败: %w", "create runstate tracker failed: %w"), err)
	}
	if err := tracker.Acquire(gc.MaxTasks); err != nil {
		if runstate.IsConcurrencyError(err) {
			ce := err.(*runstate.ConcurrencyError)
			return fmt.Errorf(i18n.T(
				"本机并发任务已达上限 (%d/%d)，请等待其他任务完成，或调整 ~/.skating/config.yaml 的 max_tasks",
				"local concurrent task limit reached (%d/%d), wait for other tasks to finish, or adjust max_tasks in ~/.skating/config.yaml"),
				ce.Running, ce.Limit)
		}
		return fmt.Errorf(i18n.T("占用并发槽位失败: %w", "acquire concurrency slot failed: %w"), err)
	}
	tracker.StartHeartbeat()

	// pipeline 执行结束后写入终态；只有整体状态确定时才写（success 时顺带更新耗时预估基准）
	var overallStatus string
	defer func() {
		if overallStatus == "" {
			return
		}
		if err := tracker.Finish(overallStatus); err != nil {
			fmt.Fprintf(os.Stderr, i18n.T("警告: 写入运行终态失败: %v\n", "Warning: write run final state failed: %v\n"), err)
		}
	}()

	fmt.Printf(i18n.T("超时限制: %s\n", "Timeout: %s\n"), timeout)
	if exp := tracker.ExpectedEnd(); !exp.IsZero() {
		fmt.Printf(i18n.T("预计结束: %s（基于上次成功构建耗时）\n", "Expected end: %s (based on last successful build)\n"), exp.Format("15:04:05"))
	}

	// 总 step 数：按 --step 过滤后实际会执行的 step 数（供进度展示）
	totalSteps := 0
	for _, stage := range pl.Stages {
		if runOpts != nil && !runOpts.SelectsStage(stage.Name) {
			continue
		}
		if runOpts != nil && runOpts.StepName != "" {
			for _, st := range stage.Steps {
				if st.Name == runOpts.StepName {
					totalSteps++
				}
			}
		} else {
			totalSteps += len(stage.Steps)
		}
	}
	tracker.SetTotalSteps(totalSteps)

	// 超时控制：ctx 取消后 executor 内部的 shell/docker 子进程会被终止
	runCtx, cancelRun := context.WithTimeout(context.Background(), timeout)
	defer cancelRun()

	// 日志收集：同时输出到终端和缓冲区。
	// 并行 step 的流式行输出会并发调用 logFn，需加锁保护 logBuf。
	var logBuf bytes.Buffer
	var logMu sync.Mutex
	logFn := func(msg string) {
		logMu.Lock()
		defer logMu.Unlock()
		fmt.Println(msg)
		// 日志文件/插件拿到的内容去掉 ANSI 颜色序列，保持纯文本
		logBuf.WriteString(executor.StripANSI(msg))
		logBuf.WriteString("\n")
	}

	// 创建适配器：将 executor.Runner 适配到 pipeline.Executor 接口
	runner := executor.NewRunner()
	// shell 输出按行实时流式转发到 logFn
	// 注入超时 ctx：取消后 executor 内部终止 shell/docker 子进程
	runner.WithContext(runCtx)
	// 跨 step 变量池：脚本 echo "::set-var KEY=VALUE" 或 Lua set_env 写入，
	// 后续 step 的 env 与 When 条件自动可见
	vars := pipeline.NewVarPool()
	runner.OnSetVar(vars.Set)

	defer runner.CloseLua()

	// 有 step 配置了镜像但 docker 不可用时提前提示
	anyImage, hasDocker := false, runner.IsDockerAvailable()
	for _, stage := range pl.Stages {
		for _, st := range stage.Steps {
			if st.Image != "" {
				anyImage = true
			}
		}
	}
	if anyImage && !hasDocker {
		fmt.Fprint(os.Stderr, i18n.T("警告: 有步骤配置了 Docker 镜像但 host 未检测到 docker 命令，相关步骤将使用 host shell 执行。\n",
			"Warning: some steps configure Docker images but `docker` CLI not found, those steps fall back to host shell.\n"))
	}

	adapter := &progressAdapter{
		inner:   &executorAdapter{runner: runner, workDir: project.Path, secrets: s},
		tracker: tracker,
	}

	// 5. 执行流水线（goroutine + select，支持超时中断）
	type runOutcome struct {
		results []*pipeline.PipelineResult
		err     error
	}
	outcomeCh := make(chan runOutcome, 1)
	go func() {
		res, err := pipeline.RunPipeline(pl, adapter, env, logFn, runOpts, vars)
		outcomeCh <- runOutcome{results: res, err: err}
	}()

	var results []*pipeline.PipelineResult
	var runErr error
	timedOut := false
	select {
	case oc := <-outcomeCh:
		results, runErr = oc.results, oc.err
	case <-runCtx.Done():
		timedOut = true
		fmt.Println(executor.ColorFail(fmt.Sprintf(
			i18n.T("=== 构建超时: 超过 %s，正在终止子进程... ===", "=== Build TIMEOUT: exceeded %s, terminating subprocesses... ==="),
			timeout)))
		// 子进程已随 runCtx 取消被终止，等待 pipeline goroutine 退出
		graceCtx, cancelGrace := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelGrace()
		select {
		case oc := <-outcomeCh:
			results, runErr = oc.results, oc.err
		case <-graceCtx.Done():
			runErr = fmt.Errorf("pipeline did not exit after timeout")
		}
	}

	elapsed := time.Since(startTime)

	// 6. 确定整体状态
	fmt.Println()
	switch {
	case timedOut:
		overallStatus = "timeout"
	case runErr != nil:
		overallStatus = "failure"
	default:
		overallStatus = "success"
	}
	switch overallStatus {
	case "success":
		fmt.Println(executor.ColorSuccess(i18n.T("=== 构建完成: success ===", "=== Build SUCCESS ===")))
	case "timeout":
		fmt.Println(executor.ColorFail(i18n.T("=== 构建超时: timeout ===", "=== Build TIMEOUT ===")))
	default:
		fmt.Println(executor.ColorFail(fmt.Sprintf(i18n.T("=== 构建失败: %v ===", "=== Build FAILED: %v ==="), runErr)))
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
		if timedOut {
			return fmt.Errorf(i18n.T("构建超时（超过 %s），已强制终止", "build timed out (exceeded %s) and was terminated"), timeout)
		}
		return runErr
	}
	return nil
}

// progressAdapter 包装 pipeline.Executor，把 step 的开始/完成事件同步到 runstate.Tracker，
// 由心跳定期落盘 bbolt，供 skating stats 展示实时进度
type progressAdapter struct {
	inner   pipeline.Executor
	tracker *runstate.Tracker
}

func (a *progressAdapter) Execute(step pipeline.Step, env map[string]string, logFn func(string)) (string, error) {
	a.tracker.StepStart(step.StageName, step.Name)
	out, err := a.inner.Execute(step, env, logFn)
	a.tracker.StepDone()
	return out, err
}

// readProjectName 从 .skating.yaml 中解析 name 字段
func readProjectName(configPath string) string {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "name:") {
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "name:"))
			val = strings.Trim(val, `"'`)
			return val
		}
	}
	return ""
}

// gitCommitID 返回项目目录当前的 git commit id（完整 hash）。
// 项目不存在 .git（目录，或 worktree 场景下的 .git 指针文件）时返回空字符串，不视为错误。
func gitCommitID(dir string) string {
	if dir == "" {
		return ""
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return ""
	}
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// gitBranch 返回项目目录当前的 git 分支名（git rev-parse --abbrev-ref HEAD）。
// 非 git 目录或命令失败时返回空字符串，不视为错误。
func gitBranch(dir string) string {
	if dir == "" {
		return ""
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return ""
	}
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// executorAdapter 将 executor.Runner 适配到 pipeline.Executor 接口
type executorAdapter struct {
	runner  *executor.Runner
	workDir string
	// secrets 全局 secret 存储：ssh 步骤的 secret 字段是名字，
	// 执行前在这里 resolve 出真实密码/私钥，不落 yaml、不进日志
	secrets *store.Store
}

func (a *executorAdapter) Execute(step pipeline.Step, env map[string]string, logFn func(msg string)) (string, error) {
	// SKA_GIT_BRANCH：每个 step 运行前实时查询当前分支，保证切分支后立即生效。
	// 克隆 env 避免污染共享 map（并行 stage 下多个 step 并发读取同一 map）；
	// 用户通过 --env 显式注入的同名变量优先，系统不覆盖；非 git 目录不注入。
	stepEnv := env
	if _, userSet := env["SKA_GIT_BRANCH"]; !userSet {
		if branch := gitBranch(a.workDir); branch != "" {
			stepEnv = maps.Clone(env)
			stepEnv["SKA_GIT_BRANCH"] = branch
		}
	}
	es := executor.Step{
		Name:          step.Name,
		Type:          step.Type,
		Script:        step.Script,
		Source:        step.Source,
		Image:         step.Image,
		DockerOptions: step.DockerOptions,
		WorkDir:       step.WorkDir,
		Host:          step.Host,
		Port:          step.Port,
		User:          step.User,
		Auth:          step.Auth,
		Direction:     step.Direction,
		Local:         step.Local,
		Remote:        step.Remote,
		Delete:        step.Delete,
	}
	// ssh/scp/sync 步骤：把 yaml 中的 secret 名字 resolve 成真实认证材料。
	// 失败时直接让 step 失败，错误信息只含 secret 名，不泄露值
	if step.Type == "ssh" || step.Type == "scp" || step.Type == "sync" {
		val, err := a.resolveSecret(step.Name, step.Secret)
		if err != nil {
			return "", err
		}
		es.Secret = val
	}
	return a.runner.Execute(es, a.workDir, stepEnv, logFn)
}

// resolveSecret 从全局 secret 存储读取 ssh 认证材料
func (a *executorAdapter) resolveSecret(stepName, secretName string) (string, error) {
	if secretName == "" {
		return "", fmt.Errorf("step %q: ssh secret is required (skating secret set <name>, then set secret: <name> in yaml)", stepName)
	}
	if a.secrets == nil {
		return "", fmt.Errorf("step %q: secret store unavailable", stepName)
	}
	val, err := a.secrets.GetSecret(secretName)
	if err != nil {
		return "", fmt.Errorf("step %q: resolve secret %q: %w", stepName, secretName, err)
	}
	return val, nil
}

// printSummary 打印构建摘要
func printSummary(results []*pipeline.PipelineResult, elapsed time.Duration) {
	if len(results) == 0 {
		return
	}

	fmt.Println(executor.ColorSubtitle(i18n.T("--- 构建摘要 ---", "--- Build Summary ---")))
	for _, r := range results {
		icon := "✓"
		statusColor := executor.ColorSuccess
		if r.Status == "failed" {
			icon = "✗"
			statusColor = executor.ColorFail
		} else if r.Status == "skipped" {
			icon = "○"
			statusColor = executor.ColorWarn
		}
		fmt.Println(statusColor(fmt.Sprintf("  [%s/%s] %s %s (%s)", r.StageName, r.StepName, icon, r.Status, r.Duration)))
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

// splitGroupArg 解析位置参数 <project>+<group> 形式（按第一个 '+' 切分）。
//
//	"prj+grp" → ("prj", "grp", true)
//	"+grp"    → ("",    "grp", true)
//	"prj"     → ("prj", "",    false)
//	""        → ("",    "",    false)
//
// 注意：项目名本身含 '+' 时会被误解析为分隔符；项目名来自目录名或 yaml name，
// 实际不会含 '+'，暂不做转义。
func splitGroupArg(arg string) (project, group string, hasSep bool) {
	project, group, hasSep = strings.Cut(arg, "+")
	return
}

// parseRunGroup 把 --group <name> 解析成 RunOptions（Stages 集合）。
//
// 不允许的情况:
//   - .skating.yaml 未定义任何 pipeline.groups → 报错
//   - 组名不存在 → 报错并列出已知组名（排序）
//
// 组到 stage 的映射在 LoadConfig 阶段已校验（组内条目必须是已声明 stage）。
func parseRunGroup(raw string, cfg *pipeline.Config) (*pipeline.RunOptions, error) {
	groups := cfg.Pipeline.Groups
	if len(groups) == 0 {
		return nil, errors.New(i18n.T(
			"当前 .skating.yaml 未定义任何 pipeline.groups",
			"no pipeline.groups defined in .skating.yaml"))
	}

	refs, ok := groups[raw]
	if !ok {
		known := slices.Sorted(maps.Keys(groups))
		return nil, fmt.Errorf(i18n.T(
			"--group %q 不存在。已知 group: %v",
			"group %q not found. Known groups: %v"),
			raw, known)
	}

	set := make(map[string]bool, len(refs.Stages))
	for _, name := range refs.Stages {
		set[name] = true
	}
	return &pipeline.RunOptions{Stages: set}, nil
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

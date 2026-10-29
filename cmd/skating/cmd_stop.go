package main

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/hpgood/skating/internal/i18n"
	"github.com/hpgood/skating/internal/runstate"
	"github.com/spf13/cobra"
)

// errProcessGone 目标进程已不存在（僵尸记录，直接清理即可）
var errProcessGone = errors.New("process already gone")

var stopCmd = &cobra.Command{
	Use:   "stop [project-name] <build-id>",
	Short: i18n.T("停止正在运行的任务，或清理僵尸任务记录", "Stop a running task, or clean a stale (zombie) record"),
	Long: i18n.T(
		`停止本机正在运行的构建，或清理进程已退出的僵尸记录。

用法:
  skating stop <build-id>             项目名默认取当前目录
  skating stop <project-name> <build-id>

说明:
  - 记录为 running 且心跳新鲜 → 终止对应进程，记录标记为 stopped
  - 记录心跳超时（stale / 进程已退出）→ 直接删除该僵尸记录
  - 记录已结束（success/failure/timeout/stopped）→ 无需处理

示例:
  skating stats --once                先查看 PROJECT / BUILD#
  skating stop 102
  skating stop training 102`,
		`Stop a running build on this host, or clean a stale (zombie) record whose process is gone.

Usage:
  skating stop <build-id>             project defaults to the current directory
  skating stop <project-name> <build-id>

Behavior:
  - running with a fresh heartbeat -> terminate the process and mark the record stopped
  - heartbeat timed out (stale / process exited) -> delete the zombie record
  - already finished (success/failure/timeout/stopped) -> nothing to do

Examples:
  skating stats --once                find the PROJECT / BUILD# first
  skating stop 102
  skating stop training 102`),
	Args: cobra.MaximumNArgs(2),
	RunE: runStop,
}

func runStop(cmd *cobra.Command, args []string) error {
	project, buildID, err := resolveStopTarget(args)
	if err != nil {
		return err
	}

	st, err := runstate.LocalState(project, buildID)
	if err != nil {
		if errors.Is(err, runstate.ErrNotFound) {
			return fmt.Errorf(i18n.T(
				"找不到项目 %q 的构建 #%d（用 skating stats 确认 PROJECT / BUILD#）",
				"no build found for project %q (build #%d; check PROJECT / BUILD# with skating stats)"),
				project, buildID)
		}
		return fmt.Errorf(i18n.T("读取运行状态失败: %w", "read runstate failed: %w"), err)
	}

	switch {
	case runstate.IsStale(*st):
		// 心跳超时：进程已崩溃/被强杀，只清理残留记录
		if err := runstate.DeleteRecord(st.ID); err != nil {
			return fmt.Errorf(i18n.T("清理僵尸记录失败: %w", "delete stale record failed: %w"), err)
		}
		fmt.Printf(i18n.T("已清理僵尸记录: %s #%d（进程已退出）\n",
			"Removed stale record: %s #%d (process already gone)\n"), project, buildID)

	case st.Status == runstate.StatusRunning:
		// 正在运行：终止进程
		if err := stopProcess(st.PID); err != nil {
			if errors.Is(err, errProcessGone) {
				_ = runstate.DeleteRecord(st.ID)
				fmt.Printf(i18n.T("进程已不存在，已清理记录: %s #%d\n",
					"Process already gone; removed record: %s #%d\n"), project, buildID)
				return nil
			}
			return fmt.Errorf(i18n.T("停止进程 %d 失败: %w", "failed to stop process %d: %w"), st.PID, err)
		}
		if err := runstate.SetStatus(st.ID, runstate.StatusStopped); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), i18n.T("警告: 更新运行状态失败: %v\n",
				"Warning: update run state failed: %v\n"), err)
		}
		fmt.Printf(i18n.T("已停止: %s #%d (pid %d)\n",
			"Stopped: %s #%d (pid %d)\n"), project, buildID, st.PID)

	default:
		fmt.Printf(i18n.T("构建 %s #%d 已结束 (%s)，无需停止\n",
			"Build %s #%d already finished (%s); nothing to stop\n"), project, buildID, st.Status)
	}
	return nil
}

// resolveStopTarget 解析 [project-name] <build-id>：
//   - 1 个参数 → 该参数为 build-id，项目名取当前目录
//   - 2 个参数 → 第一个是项目名，第二个是 build-id
func resolveStopTarget(args []string) (project string, buildID int, err error) {
	switch len(args) {
	case 1:
		project = defaultProjectName()
		buildID, err = strconv.Atoi(args[0])
	case 2:
		project = args[0]
		buildID, err = strconv.Atoi(args[1])
	default:
		return "", 0, errors.New(i18n.T(
			"用法: skating stop [project-name] <build-id>",
			"usage: skating stop [project-name] <build-id>"))
	}
	if err != nil {
		return "", 0, fmt.Errorf(i18n.T("无效的 build-id %q（应为整数）", "invalid build-id %q (must be an integer)"),
			args[len(args)-1])
	}
	if project == "" {
		return "", 0, errors.New(i18n.T(
			"无法确定项目名，请显式传入: skating stop <project-name> <build-id>",
			"cannot determine project name; pass it explicitly: skating stop <project-name> <build-id>"))
	}
	return project, buildID, nil
}

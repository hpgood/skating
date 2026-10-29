package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/hpgood/skating/internal/executor"
	"github.com/hpgood/skating/internal/i18n"
	"github.com/hpgood/skating/internal/runstate"
	"github.com/hpgood/skating/internal/store"
	"github.com/spf13/cobra"
)

var (
	statsAll   bool // 附带历史终态记录
	statsOnce  bool // 单次输出后退出（默认持续刷新）
	statsPrune bool // 清理僵尸任务（stale）记录
)

var statsCmd = &cobra.Command{
	Use:   "stats",
	Short: i18n.T("查看正在运行的任务及进度", "Show running tasks and their progress"),
	RunE:  runStats,
}

func init() {
	statsCmd.Flags().BoolVar(&statsAll, "all", false, i18n.T("附带最近的历史终态记录", "also show recent finished records"))
	statsCmd.Flags().BoolVar(&statsOnce, "once", false, i18n.T("只输出一次后退出（默认持续刷新，Ctrl+C 退出）", "render once and exit (default keeps refreshing until Ctrl+C)"))
	statsCmd.Flags().BoolVar(&statsPrune, "prune", false, i18n.T("清理僵尸任务记录（心跳超时的残留，进程已退出）", "prune stale task records (heartbeat timed out, process already gone)"))
}

func runStats(cmd *cobra.Command, args []string) error {
	s, err := store.NewStore()
	if err != nil {
		return fmt.Errorf(i18n.T("创建 store 失败: %w", "create store failed: %w"), err)
	}
	gc, err := s.GetGlobalConfig()
	if err != nil {
		return fmt.Errorf(i18n.T("读取全局配置失败: %w", "load global config failed: %w"), err)
	}

	// --prune：清理心跳超时的僵尸任务记录（进程已退出但记录残留）。
	// 这些记录本就不占用并发槽位，但会一直显示在列表里。
	if statsPrune {
		n, err := runstate.PruneStale()
		if err != nil {
			return fmt.Errorf(i18n.T("清理僵尸任务失败: %w", "prune stale tasks failed: %w"), err)
		}
		fmt.Printf(i18n.T("已清理 %d 个僵尸任务记录\n", "Pruned %d stale task record(s)\n"), n)
	}

	if statsOnce {
		return renderStats(gc.MaxTasks)
	}

	// 持续刷新模式：每 2s 清屏重绘，Ctrl+C 退出
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		fmt.Print("\033[H\033[2J")
		if err := renderStats(gc.MaxTasks); err != nil {
			return err
		}
		fmt.Println(i18n.T("(每 2s 刷新，Ctrl+C 退出)", "(refreshes every 2s, Ctrl+C to exit)"))
		select {
		case <-ticker.C:
		case <-ctx.Done():
			fmt.Println(i18n.T("\n已退出", "\nexited"))
			return nil
		}
	}
}

func renderStats(maxTasks int) error {
	states, err := runstate.Snapshot()
	if err != nil {
		return fmt.Errorf(i18n.T("读取运行状态失败: %w", "read runstate failed: %w"), err)
	}

	now := time.Now()

	// running 只统计真正占用槽位的任务（stale 不占槽位，但仍列出以便排查）
	var live []runstate.State // running / stale
	var done []runstate.State // 终态记录
	running := 0
	for _, st := range states {
		switch st.Status {
		case runstate.StatusRunning:
			running++
			live = append(live, st)
		case runstate.StatusStale:
			live = append(live, st)
		default:
			done = append(done, st)
		}
	}
	// 终态按开始时间倒序
	sort.Slice(done, func(i, j int) bool { return done[i].StartTime.After(done[j].StartTime) })

	fmt.Println(executor.ColorTitle(fmt.Sprintf(i18n.T(
		"并发占用: %d/%d\n", "Concurrency: %d/%d\n"),
		running, maxTasks)))

	if len(live) == 0 {
		fmt.Println(i18n.T("当前没有正在运行的任务", "No tasks are currently running"))
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, i18n.T(
		"项目\tBuild#\t状态\t进度\t当前步骤\t已运行\t预计剩余\t开始时间",
		"PROJECT\tBUILD#\tSTATUS\tPROGRESS\tSTEP\tELAPSED\tREMAINING\tSTARTED"))
	for _, st := range live {
		// 进度百分比：优先用历史耗时估算（避免长步骤期间一直显示 0%），
		// 任务未完成时不超过 99%
		estPct := -1
		if !st.ExpectedEnd.IsZero() {
			total := st.ExpectedEnd.Sub(st.StartTime)
			if total > 0 {
				estPct = int(float64(now.Sub(st.StartTime)) / float64(total) * 100)
				if estPct < 1 {
					estPct = 1
				}
				if estPct > 99 {
					estPct = 99
				}
			}
		}

		progress := "-"
		if st.TotalSteps > 0 {
			pct := st.DoneSteps * 100 / st.TotalSteps
			if pct > 99 {
				pct = 99 // running 状态不显示 100%
			}
			if estPct >= 0 {
				pct = estPct
			}
			progress = fmt.Sprintf("%d/%d (%d%%)", st.DoneSteps, st.TotalSteps, pct)
		} else if estPct >= 0 {
			progress = fmt.Sprintf("~%d%%", estPct)
		}
		cur := st.CurStep
		if st.CurStage != "" {
			cur = st.CurStage + "/" + st.CurStep
		}
		if cur == "" {
			cur = "-"
		}
		elapsed := now.Sub(st.StartTime).Truncate(time.Second)

		remaining := "-"
		if !st.ExpectedEnd.IsZero() {
			d := st.ExpectedEnd.Sub(now).Truncate(time.Second)
			if d > 0 {
				remaining = "~" + d.String()
			} else {
				remaining = executor.ColorWarn(i18n.T("已超预估!", "over estimate!"))
			}
		}
		status := st.Status
		if st.Status == runstate.StatusStale {
			status = executor.ColorWarn(status)
		}
		fmt.Fprintf(w, "%s\t%d\t%s\t%s\t%s\t%s\t%s\t%s\n",
			st.Project, st.BuildID, status, progress, cur, elapsed, remaining,
			st.StartTime.Format("15:04:05"))
	}
	w.Flush()

	if statsAll && len(done) > 0 {
		fmt.Println(executor.ColorSubtitle(i18n.T("\n--- 最近历史 ---", "\n--- Recent History ---")))
		w2 := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w2, i18n.T(
			"项目\tBuild#\t状态\t耗时\t结束时间",
			"PROJECT\tBUILD#\tSTATUS\tDURATION\tENDED"))
		const maxHistory = 20
		for i, st := range done {
			if i >= maxHistory {
				break
			}
			dur := st.LastUpdate.Sub(st.StartTime).Truncate(time.Second)
			status := st.Status
			switch status {
			case runstate.StatusSuccess:
				status = executor.ColorSuccess(status)
			case runstate.StatusFailure, runstate.StatusTimeout:
				status = executor.ColorFail(status)
			case runstate.StatusStopped:
				status = executor.ColorWarn(status)
			}
			fmt.Fprintf(w2, "%s\t%d\t%s\t%s\t%s\n",
				st.Project, st.BuildID, status, dur, st.LastUpdate.Format("01-02 15:04:05"))
		}
		w2.Flush()
	}
	return nil
}

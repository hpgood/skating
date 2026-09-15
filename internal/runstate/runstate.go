// Package runstate 基于 bbolt 记录本机任务运行状态：
//
//   - runs bucket:      key=<host>/<project>/<buildid>, value=State(JSON)
//   - durations bucket: key=<host>/<project>, value=上次成功构建总耗时（纳秒）
//
// 并发闸门说明：bbolt 对读写打开的 DB 持有独占文件锁（同一时刻仅一个进程可持有），
// 因此本包采用「每次操作短暂打开-写-关闭」的模式，并在单个 Update 写事务内完成
// 「统计 running 数量 → 校验上限 → 插入记录」。bbolt 写事务本身是串行的，
// 多个 skating run 进程之间天然无竞态。
package runstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
)

// 任务状态
const (
	StatusRunning = "running"
	StatusSuccess = "success"
	StatusFailure = "failure"
	StatusTimeout = "timeout"
	// StatusStale running 记录心跳超时后的展示态（进程可能已崩溃）
	StatusStale = "stale"
)

const (
	// HeartbeatInterval 心跳刷新间隔
	HeartbeatInterval = 3 * time.Second
	// StaleAfter 心跳超过该时长未刷新视为 stale（进程崩溃/强杀残留）
	StaleAfter = 15 * time.Second
	// dbOpenTimeout 每次打开 DB 的文件锁等待上限
	dbOpenTimeout = 5 * time.Second
	// pruneAge 终态记录保留时长，超过后顺带清理
	pruneAge = 7 * 24 * time.Hour
)

var (
	runsBucket      = []byte("runs")
	durationsBucket = []byte("durations")
)

// State 是一条任务运行记录
type State struct {
	ID          string    `json:"id"`           // <host>/<project>/<buildid>
	Host        string    `json:"host"`         // 主机名（并发闸门按本机统计）
	PID         int       `json:"pid"`          // 运行进程 PID
	Project     string    `json:"project"`      // 项目名
	BuildID     int       `json:"buildid"`      // 构建编号
	Status      string    `json:"status"`       // running | success | failure | timeout | stale
	StartTime   time.Time `json:"start_time"`   // 开始时间
	ExpectedEnd time.Time `json:"expected_end"` // 预计结束时间（now + 上次成功耗时），无历史则为零值
	LastUpdate  time.Time `json:"last_update"`  // 心跳时间戳
	CurStage    string    `json:"cur_stage"`    // 当前执行到的 stage
	CurStep     string    `json:"cur_step"`     // 当前执行到的 step
	DoneSteps   int       `json:"done_steps"`   // 已完成 step 数
	TotalSteps  int       `json:"total_steps"`  // 总 step 数
}

// ConcurrencyError 并发闸门超限错误
type ConcurrencyError struct {
	Limit   int
	Running int
}

func (e *ConcurrencyError) Error() string {
	return fmt.Sprintf("concurrent task limit reached: %d/%d running", e.Running, e.Limit)
}

// IsConcurrencyError 判断 err 是否为并发闸门超限错误
func IsConcurrencyError(err error) bool {
	var ce *ConcurrencyError
	return errors.As(err, &ce)
}

// baseDirOverride 仅测试用：非空时覆盖默认的 ~/.skating 基目录
var baseDirOverride string

// dbPath 返回 runstate 数据库路径 ~/.skating/runstate.db
func dbPath() (string, error) {
	if baseDirOverride != "" {
		return filepath.Join(baseDirOverride, "runstate.db"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home dir: %w", err)
	}
	dir := filepath.Join(home, ".skating")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("create base dir: %w", err)
	}
	return filepath.Join(dir, "runstate.db"), nil
}

// withDB 短暂打开数据库执行 fn 后立即关闭，避免长期独占文件锁
func withDB(fn func(db *bolt.DB) error) error {
	path, err := dbPath()
	if err != nil {
		return err
	}
	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: dbOpenTimeout})
	if err != nil {
		return fmt.Errorf("open runstate db: %w", err)
	}
	defer db.Close()
	return fn(db)
}

func ensureBuckets(tx *bolt.Tx) error {
	for _, b := range [][]byte{runsBucket, durationsBucket} {
		if _, err := tx.CreateBucketIfNotExists(b); err != nil {
			return fmt.Errorf("create bucket %s: %w", b, err)
		}
	}
	return nil
}

// Tracker 负责单个运行中任务的记录：注册/心跳/进度/终态
type Tracker struct {
	id   string
	host string

	mu        sync.Mutex
	st        State
	stop      chan struct{}
	done      chan struct{}
	hbStarted bool
	hbStopped bool
}

// Open 创建任务追踪器（不写库）。调用 Acquire 后记录才真正落库。
func Open(project string, buildID int) (*Tracker, error) {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	t := &Tracker{
		id:   fmt.Sprintf("%s/%s/%d", host, project, buildID),
		host: host,
	}
	t.st = State{
		ID:        t.id,
		Host:      host,
		PID:       os.Getpid(),
		Project:   project,
		BuildID:   buildID,
		StartTime: time.Now(),
	}
	return t, nil
}

// Acquire 占用一个并发槽位并写入 running 记录。
// 整个「统计本机 running → 校验 maxTasks → 插入」在同一个 bbolt 写事务内完成，
// 跨进程原子。超限返回 *ConcurrencyError。
// 同时按 durations 里的上次成功耗时估算 ExpectedEnd。
func (t *Tracker) Acquire(maxTasks int) error {
	return withDB(func(db *bolt.DB) error {
		return db.Update(func(tx *bolt.Tx) error {
			if err := ensureBuckets(tx); err != nil {
				return err
			}
			rb := tx.Bucket(runsBucket)
			now := time.Now()

			var markStale []string
			var pruneKeys []string
			count := 0
			c := rb.Cursor()
			for k, v := c.First(); k != nil; k, v = c.Next() {
				var s State
				if json.Unmarshal(v, &s) != nil {
					continue
				}
				if s.Status != StatusRunning {
					// 顺带清理过老的历史终态记录
					if now.Sub(s.LastUpdate) > pruneAge {
						pruneKeys = append(pruneKeys, string(k))
					}
					continue
				}
				if s.Host != t.host {
					continue // 并发上限只统计本机
				}
				if now.Sub(s.LastUpdate) > StaleAfter {
					// 心跳超时的残留记录：标记 stale，不占用槽位
					markStale = append(markStale, string(k))
					continue
				}
				count++
			}
			// 光标遍历后再应用写操作，避免边遍历边写
			for _, key := range markStale {
				if raw := rb.Get([]byte(key)); raw != nil {
					var s State
					if json.Unmarshal(raw, &s) == nil {
						s.Status = StatusStale
						if buf, err := json.Marshal(s); err == nil {
							_ = rb.Put([]byte(key), buf)
						}
					}
				}
			}
			for _, key := range pruneKeys {
				_ = rb.Delete([]byte(key))
			}

			if maxTasks > 0 && count >= maxTasks {
				return &ConcurrencyError{Limit: maxTasks, Running: count}
			}

			// 读取上次成功耗时，估算预计结束时间
			var lastDur time.Duration
			if raw := tx.Bucket(durationsBucket).Get([]byte(t.host + "/" + t.st.Project)); raw != nil {
				if n, err := strconv.ParseInt(string(raw), 10, 64); err == nil && n > 0 {
					lastDur = time.Duration(n)
				}
			}

			t.mu.Lock()
			t.st.Status = StatusRunning
			t.st.LastUpdate = now
			if lastDur > 0 {
				t.st.ExpectedEnd = now.Add(lastDur)
			}
			st := t.st
			t.mu.Unlock()

			buf, err := json.Marshal(st)
			if err != nil {
				return fmt.Errorf("marshal state: %w", err)
			}
			return rb.Put([]byte(t.id), buf)
		})
	})
}

// SetTotalSteps 设置总 step 数（按 --step 过滤后实际会执行的 step 数）
func (t *Tracker) SetTotalSteps(total int) {
	t.mu.Lock()
	t.st.TotalSteps = total
	t.mu.Unlock()
}

// StepStart 标记当前开始执行的 stage/step
func (t *Tracker) StepStart(stage, step string) {
	t.mu.Lock()
	t.st.CurStage = stage
	t.st.CurStep = step
	t.mu.Unlock()
}

// StepDone 累加已完成 step 计数
func (t *Tracker) StepDone() {
	t.mu.Lock()
	t.st.DoneSteps++
	t.mu.Unlock()
}

// ExpectedEnd 返回预计结束时间（零值表示无历史耗时可比估算）
func (t *Tracker) ExpectedEnd() time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.st.ExpectedEnd
}

// StartHeartbeat 启动后台心跳，每 3s 将状态（含进度）刷新到 bbolt
func (t *Tracker) StartHeartbeat() {
	t.mu.Lock()
	if t.hbStarted {
		t.mu.Unlock()
		return
	}
	t.hbStarted = true
	t.stop = make(chan struct{})
	t.done = make(chan struct{})
	stop, done := t.stop, t.done
	t.mu.Unlock()

	go func() {
		defer close(done)
		ticker := time.NewTicker(HeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				_ = t.flush()
			}
		}
	}()
}

// flush 将内存状态快照写库（心跳）
func (t *Tracker) flush() error {
	t.mu.Lock()
	if t.st.Status != StatusRunning {
		t.mu.Unlock()
		return nil
	}
	t.st.LastUpdate = time.Now()
	st := t.st
	t.mu.Unlock()

	return withDB(func(db *bolt.DB) error {
		return db.Update(func(tx *bolt.Tx) error {
			rb := tx.Bucket(runsBucket)
			if rb == nil {
				return nil
			}
			buf, err := json.Marshal(st)
			if err != nil {
				return err
			}
			return rb.Put([]byte(t.id), buf)
		})
	})
}

// Finish 写入终态（success/failure/timeout），停止心跳。
// 仅 success 时更新 durations 估算基准（失败/超时耗时不作为下次预估依据）。
func (t *Tracker) Finish(status string) error {
	t.stopHeartbeat()

	t.mu.Lock()
	t.st.Status = status
	end := time.Now()
	t.st.LastUpdate = end
	st := t.st
	t.mu.Unlock()

	return withDB(func(db *bolt.DB) error {
		return db.Update(func(tx *bolt.Tx) error {
			if err := ensureBuckets(tx); err != nil {
				return err
			}
			rb := tx.Bucket(runsBucket)
			buf, err := json.Marshal(st)
			if err != nil {
				return fmt.Errorf("marshal state: %w", err)
			}
			if err := rb.Put([]byte(t.id), buf); err != nil {
				return err
			}
			if status == StatusSuccess {
				d := end.Sub(st.StartTime)
				return tx.Bucket(durationsBucket).Put(
					[]byte(st.Host+"/"+st.Project),
					[]byte(strconv.FormatInt(int64(d), 10)),
				)
			}
			return nil
		})
	})
}

// stopHeartbeat 停止后台心跳并等待其退出
func (t *Tracker) stopHeartbeat() {
	t.mu.Lock()
	if !t.hbStarted || t.hbStopped {
		t.mu.Unlock()
		return
	}
	t.hbStopped = true
	stop, done := t.stop, t.done
	t.mu.Unlock()

	close(stop)
	select {
	case <-done:
	case <-time.After(8 * time.Second):
	}
}

// Snapshot 返回所有运行记录。running 且心跳过期（> StaleAfter）的记录
// 在返回值中标记为 stale（只改展示值，不动库）。
func Snapshot() ([]State, error) {
	var out []State
	err := withDB(func(db *bolt.DB) error {
		return db.View(func(tx *bolt.Tx) error {
			rb := tx.Bucket(runsBucket)
			if rb == nil {
				return nil
			}
			now := time.Now()
			return rb.ForEach(func(k, v []byte) error {
				var s State
				if json.Unmarshal(v, &s) != nil {
					return nil
				}
				if s.Status == StatusRunning && now.Sub(s.LastUpdate) > StaleAfter {
					s.Status = StatusStale
				}
				out = append(out, s)
				return nil
			})
		})
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

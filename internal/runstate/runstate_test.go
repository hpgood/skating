package runstate

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

// useTempBase 将 runstate 数据库指到临时目录
func useTempBase(t *testing.T) {
	t.Helper()
	baseDirOverride = t.TempDir()
	t.Cleanup(func() { baseDirOverride = "" })
}

func findState(states []State, id string) *State {
	for i := range states {
		if states[i].ID == id {
			return &states[i]
		}
	}
	return nil
}

// Acquire 写入 running 记录，Finish(success) 后 durations 更新，
// 下一个任务 Acquire 时能据此估算 ExpectedEnd。
func TestAcquireFinishAndEstimate(t *testing.T) {
	useTempBase(t)

	t1, err := Open("demo", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := t1.Acquire(2); err != nil {
		t.Fatalf("acquire: %v", err)
	}

	// running 记录可见
	states, err := Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	st := findState(states, t1.id)
	if st == nil || st.Status != StatusRunning {
		t.Fatalf("expected running record, got %+v", st)
	}
	// 首次运行无历史，ExpectedEnd 应为零值
	if !st.ExpectedEnd.IsZero() {
		t.Fatalf("expected zero ExpectedEnd on first run, got %v", st.ExpectedEnd)
	}

	// 模拟运行了 2 分钟后成功结束
	t1.mu.Lock()
	t1.st.StartTime = time.Now().Add(-2 * time.Minute)
	t1.mu.Unlock()
	if err := t1.Finish(StatusSuccess); err != nil {
		t.Fatalf("finish: %v", err)
	}

	// 第二个任务启动，应基于上次耗时 2m 估算 ExpectedEnd
	t2, err := Open("demo", 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := t2.Acquire(2); err != nil {
		t.Fatalf("acquire #2: %v", err)
	}
	if exp := t2.ExpectedEnd(); exp.IsZero() || time.Until(exp) < time.Minute {
		t.Fatalf("expected ExpectedEnd ~2m in future, got %v", exp)
	}

	// 失败终态不应覆盖 durations 估算基准
	t2.SetTotalSteps(3)
	t2.StepStart("build", "compile")
	t2.StepDone()
	if err := t2.Finish(StatusFailure); err != nil {
		t.Fatalf("finish #2: %v", err)
	}

	// 终态记录可见
	states, err = Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if got := findState(states, t2.id); got == nil || got.Status != StatusFailure || got.DoneSteps != 1 {
		t.Fatalf("unexpected finished record: %+v", got)
	}
}

// 并发闸门：maxTasks=1 时第二个任务被拒绝；第一个结束后可再次占用
func TestConcurrencyLimit(t *testing.T) {
	useTempBase(t)

	t1, _ := Open("app", 1)
	if err := t1.Acquire(1); err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	t2, _ := Open("app", 2)
	err := t2.Acquire(1)
	if !IsConcurrencyError(err) {
		t.Fatalf("expected concurrency error, got %v", err)
	}
	var ce *ConcurrencyError
	if !errors.As(err, &ce) || ce.Limit != 1 || ce.Running != 1 {
		t.Fatalf("unexpected concurrency error: %v", err)
	}

	// 第一个任务结束释放槽位
	if err := t1.Finish(StatusSuccess); err != nil {
		t.Fatal(err)
	}
	if err := t2.Acquire(1); err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
}

// 心跳过期的 running 残留记录应标记为 stale，且不再占用并发槽位
func TestStaleRecord(t *testing.T) {
	useTempBase(t)

	// 手工植入一条心跳过期 1 小时的 running 记录
	old, _ := Open("legacy", 9)
	oldSt := old.st
	oldSt.Status = StatusRunning
	oldSt.StartTime = time.Now().Add(-time.Hour)
	oldSt.LastUpdate = time.Now().Add(-time.Hour)
	putState(t, oldSt)

	// maxTasks=1，但 stale 记录不占槽位 → 新任务可以 Acquire
	fresh, _ := Open("app", 1)
	if err := fresh.Acquire(1); err != nil {
		t.Fatalf("acquire should succeed despite stale record: %v", err)
	}

	states, err := Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if got := findState(states, old.id); got == nil || got.Status != StatusStale {
		t.Fatalf("expected stale status, got %+v", got)
	}
}

// PruneStale 只删除心跳过期的僵尸记录，正常 running 与终态记录保留
func TestPruneStale(t *testing.T) {
	useTempBase(t)

	// 1) 心跳过期 1 小时的残留（僵尸）
	zombie, _ := Open("legacy", 9)
	zs := zombie.st
	zs.Status = StatusRunning
	zs.StartTime = time.Now().Add(-time.Hour)
	zs.LastUpdate = time.Now().Add(-time.Hour)
	putState(t, zs)

	// 2) 正常的 running 记录（心跳新鲜）
	fresh, _ := Open("app", 1)
	if err := fresh.Acquire(10); err != nil {
		t.Fatalf("acquire: %v", err)
	}

	// 3) 终态记录
	fin, _ := Open("app", 2)
	fs := fin.st
	fs.Status = StatusSuccess
	fs.LastUpdate = time.Now()
	putState(t, fs)

	removed, err := PruneStale()
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}

	states, err := Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if findState(states, zombie.id) != nil {
		t.Errorf("zombie record should have been pruned")
	}
	if findState(states, fresh.id) == nil {
		t.Errorf("fresh running record should be kept")
	}
	if findState(states, fin.id) == nil {
		t.Errorf("finished record should be kept")
	}
}

// IsStale 判定：新鲜的 running 不算僵尸；心跳过期的 running 与已标记 stale 的算；
// 终态记录不算。
func TestIsStale(t *testing.T) {
	now := time.Now()
	if IsStale(State{Status: StatusRunning, LastUpdate: now}) {
		t.Error("fresh running should not be stale")
	}
	if !IsStale(State{Status: StatusRunning, LastUpdate: now.Add(-time.Hour)}) {
		t.Error("old running should be stale")
	}
	if !IsStale(State{Status: StatusStale, LastUpdate: now}) {
		t.Error("stale status should be stale")
	}
	if IsStale(State{Status: StatusSuccess, LastUpdate: now.Add(-time.Hour)}) {
		t.Error("finished record should not be stale")
	}
}

// LocalState / SetStatus / DeleteRecord 基本行为
func TestLocalStateSetStatusDelete(t *testing.T) {
	useTempBase(t)

	tr, _ := Open("app", 7)
	tr.st.Status = StatusRunning
	tr.st.StartTime = time.Now().Add(-time.Hour)
	tr.st.LastUpdate = time.Now().Add(-time.Hour)
	putState(t, tr.st)

	got, err := LocalState("app", 7)
	if err != nil {
		t.Fatalf("LocalState: %v", err)
	}
	if got.ID != tr.id || got.Project != "app" || got.BuildID != 7 {
		t.Fatalf("unexpected record: %+v", got)
	}

	if _, err := LocalState("app", 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing record should return ErrNotFound, got %v", err)
	}

	if err := SetStatus(tr.id, StatusStopped); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	if got, _ := LocalState("app", 7); got.Status != StatusStopped {
		t.Fatalf("status = %q, want stopped", got.Status)
	}

	if err := DeleteRecord(tr.id); err != nil {
		t.Fatalf("DeleteRecord: %v", err)
	}
	if _, err := LocalState("app", 7); !errors.Is(err, ErrNotFound) {
		t.Fatalf("record should be deleted, got %v", err)
	}
	if err := DeleteRecord(tr.id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing should return ErrNotFound, got %v", err)
	}
	if err := SetStatus(tr.id, StatusStopped); !errors.Is(err, ErrNotFound) {
		t.Fatalf("set status on missing should return ErrNotFound, got %v", err)
	}
}

// putState 直接写库（绕过 flush 的时间戳刷新），测试用
func putState(t *testing.T, st State) {
	t.Helper()
	if err := withDB(func(db *bolt.DB) error {
		return db.Update(func(tx *bolt.Tx) error {
			if err := ensureBuckets(tx); err != nil {
				return err
			}
			buf, err := json.Marshal(st)
			if err != nil {
				return err
			}
			return tx.Bucket(runsBucket).Put([]byte(st.ID), buf)
		})
	}); err != nil {
		t.Fatal(err)
	}
}

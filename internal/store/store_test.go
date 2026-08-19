package store

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// newTestStore 创建使用 t.TempDir() 的隔离 Store，不污染真实 ~/.skating
func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	return &Store{baseDir: dir}
}

func TestStore_SaveAndGetProject(t *testing.T) {
	s := newTestStore(t)

	p := &Project{
		Name:       "demo",
		Path:       "/tmp/demo",
		Image:      "golang:1.21",
		BuildID:    0,
		LastStatus: "",
		CreatedAt:  "2026-08-18T00:00:00Z",
	}
	if err := s.SaveProject(p); err != nil {
		t.Fatalf("SaveProject failed: %v", err)
	}

	got, err := s.GetProject("demo")
	if err != nil {
		t.Fatalf("GetProject failed: %v", err)
	}
	if got.Name != "demo" || got.Image != "golang:1.21" || got.Path != "/tmp/demo" {
		t.Errorf("GetProject returned wrong project: %+v", got)
	}
}

func TestStore_GetProject_NotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.GetProject("nonexistent")
	if err == nil {
		t.Fatal("expected error for missing project, got nil")
	}
}

func TestStore_ListProjects_EmptyStore(t *testing.T) {
	s := newTestStore(t)
	projects, err := s.ListProjects()
	if err != nil {
		t.Fatalf("ListProjects on empty store: %v", err)
	}
	if len(projects) != 0 {
		t.Errorf("expected empty list, got %d projects", len(projects))
	}
}

func TestStore_SaveProject_UpdateExisting(t *testing.T) {
	s := newTestStore(t)
	p := &Project{Name: "demo", Path: "/tmp/demo", Image: "old:1.0"}
	if err := s.SaveProject(p); err != nil {
		t.Fatalf("first save: %v", err)
	}

	p.Image = "new:2.0"
	p.BuildID = 5
	if err := s.SaveProject(p); err != nil {
		t.Fatalf("update save: %v", err)
	}

	got, err := s.GetProject("demo")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if got.Image != "new:2.0" {
		t.Errorf("image not updated: got %q", got.Image)
	}
	if got.BuildID != 5 {
		t.Errorf("buildid not updated: got %d", got.BuildID)
	}

	projects, _ := s.ListProjects()
	if len(projects) != 1 {
		t.Errorf("expected 1 project (no duplicates on update), got %d", len(projects))
	}
}

func TestStore_NextBuildID_Increments(t *testing.T) {
	s := newTestStore(t)
	p := &Project{Name: "demo", Path: "/tmp/demo", BuildID: 0}
	if err := s.SaveProject(p); err != nil {
		t.Fatalf("save: %v", err)
	}

	id1, err := s.NextBuildID("demo")
	if err != nil {
		t.Fatalf("NextBuildID first call: %v", err)
	}
	if id1 != 1 {
		t.Errorf("first build ID = %d, want 1", id1)
	}

	id2, err := s.NextBuildID("demo")
	if err != nil {
		t.Fatalf("NextBuildID second call: %v", err)
	}
	if id2 != 2 {
		t.Errorf("second build ID = %d, want 2", id2)
	}

	id3, err := s.NextBuildID("demo")
	if err != nil {
		t.Fatalf("NextBuildID third call: %v", err)
	}
	if id3 != 3 {
		t.Errorf("third build ID = %d, want 3", id3)
	}
}

func TestStore_NextBuildID_UnknownProject(t *testing.T) {
	s := newTestStore(t)
	_, err := s.NextBuildID("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown project, got nil")
	}
}

func TestStore_SaveAndGetLog(t *testing.T) {
	s := newTestStore(t)
	content := "build started\nstep1 ok\nbuild done\n"
	if err := s.SaveLog("demo", 1, stringReader(content)); err != nil {
		t.Fatalf("SaveLog: %v", err)
	}

	got, err := s.GetLog("demo", 1)
	if err != nil {
		t.Fatalf("GetLog: %v", err)
	}
	if got != content {
		t.Errorf("log content mismatch:\ngot: %q\nwant: %q", got, content)
	}
}

func TestStore_GetLog_NotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.GetLog("demo", 999)
	if err == nil {
		t.Fatal("expected error for missing log")
	}
}

func TestStore_ListLogs(t *testing.T) {
	s := newTestStore(t)
	for _, id := range []int{3, 1, 2} {
		if err := s.SaveLog("demo", id, stringReader("x")); err != nil {
			t.Fatalf("save log %d: %v", id, err)
		}
	}
	ids, err := s.ListLogs("demo")
	if err != nil {
		t.Fatalf("ListLogs: %v", err)
	}
	if len(ids) != 3 {
		t.Fatalf("expected 3 logs, got %d", len(ids))
	}
	wantSorted := []int{1, 2, 3}
	for i, id := range ids {
		if id != wantSorted[i] {
			t.Errorf("ids[%d] = %d, want %d (unsorted or wrong)", i, id, wantSorted[i])
		}
	}
}

func TestStore_ListLogs_IgnoresNonLogFiles(t *testing.T) {
	s := newTestStore(t)
	if err := s.SaveLog("demo", 1, stringReader("real log")); err != nil {
		t.Fatalf("save log: %v", err)
	}
	// 手动塞个非 .log 文件
	dir := filepath.Join(s.baseDir, "logs", "demo")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("noise"), 0644); err != nil {
		t.Fatalf("write noise: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "garbage.txt"), []byte("noise"), 0644); err != nil {
		t.Fatalf("write noise: %v", err)
	}
	// 塞个非数字前缀的 .log
	if err := os.WriteFile(filepath.Join(dir, "not-a-number.log"), []byte("noise"), 0644); err != nil {
		t.Fatalf("write noise: %v", err)
	}

	ids, err := s.ListLogs("demo")
	if err != nil {
		t.Fatalf("ListLogs: %v", err)
	}
	if len(ids) != 1 || ids[0] != 1 {
		t.Errorf("expected only [1], got %v", ids)
	}
}

func TestStore_CleanLogs_PreservesBuildID(t *testing.T) {
	s := newTestStore(t)
	p := &Project{Name: "demo", Path: "/tmp/demo", BuildID: 7}
	if err := s.SaveProject(p); err != nil {
		t.Fatalf("save: %v", err)
	}
	for i := 1; i <= 3; i++ {
		if err := s.SaveLog("demo", i, stringReader("x")); err != nil {
			t.Fatalf("save log %d: %v", i, err)
		}
	}

	count, err := s.CleanLogs("demo")
	if err != nil {
		t.Fatalf("CleanLogs: %v", err)
	}
	if count != 3 {
		t.Errorf("deleted count = %d, want 3", count)
	}

	// 日志目录应清空
	dir := filepath.Join(s.baseDir, "logs", "demo")
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("expected empty logs dir, got %d entries", len(entries))
	}

	// 项目 BuildID 必须保留
	got, _ := s.GetProject("demo")
	if got.BuildID != 7 {
		t.Errorf("BuildID after clean = %d, want 7 (preserved)", got.BuildID)
	}
}

func TestStore_CleanAllLogs(t *testing.T) {
	s := newTestStore(t)
	for _, name := range []string{"a", "b"} {
		if err := s.SaveLog(name, 1, stringReader("x")); err != nil {
			t.Fatalf("save %s: %v", name, err)
		}
	}

	total, err := s.CleanAllLogs()
	if err != nil {
		t.Fatalf("CleanAllLogs: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
}

func TestStore_RemoveProject(t *testing.T) {
	s := newTestStore(t)
	p := &Project{Name: "demo", Path: "/tmp/demo"}
	if err := s.SaveProject(p); err != nil {
		t.Fatalf("save: %v", err)
	}

	if err := s.RemoveProject("demo"); err != nil {
		t.Fatalf("RemoveProject: %v", err)
	}

	_, err := s.GetProject("demo")
	if err == nil {
		t.Error("project still exists after remove")
	}
}

func TestStore_RemoveProject_NotFound(t *testing.T) {
	s := newTestStore(t)
	err := s.RemoveProject("nonexistent")
	if err == nil {
		t.Fatal("expected error removing nonexistent project")
	}
}

// stringReader 简易 io.ReadCloser 包装。返回 NopCloser + 单次 Read 模式。
// store.SaveLog 用 io.Copy 读取，读到 io.EOF 时停止，所以这种一次性 reader 足够。
func stringReader(s string) io.ReadCloser {
	return io.NopCloser(&oneShotReader{s: s, pos: 0})
}

type oneShotReader struct {
	s   string
	pos int
}

func (r *oneShotReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.s) {
		return 0, io.EOF
	}
	n := copy(p, r.s[r.pos:])
	r.pos += n
	return n, nil
}
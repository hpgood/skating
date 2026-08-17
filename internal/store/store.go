package store

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Store 管理 skating 的数据持久化
type Store struct {
	baseDir string
}

// NewStore 创建一个新的 Store，自动创建 baseDir 目录
func NewStore() (*Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home dir: %w", err)
	}
	baseDir := filepath.Join(home, ".skating")
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("create base dir: %w", err)
	}
	return &Store{baseDir: baseDir}, nil
}

// saveYAML 将 data 序列化为 YAML 并写入 path
func (s *Store) saveYAML(path string, data any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create dir for %s: %w", path, err)
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create file %s: %w", path, err)
	}
	defer f.Close()

	enc := yaml.NewEncoder(f)
	enc.SetIndent(2)
	if err := enc.Encode(data); err != nil {
		return fmt.Errorf("encode yaml: %w", err)
	}
	return enc.Close()
}

// loadYAML 从 path 读取 YAML 并反序列化到 target
func (s *Store) loadYAML(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(data, target)
}

func (s *Store) projectsPath() string {
	return filepath.Join(s.baseDir, "projects.yaml")
}

// ListProjects 返回所有已注册的项目
func (s *Store) ListProjects() ([]Project, error) {
	var pf projectsFile
	if err := s.loadYAML(s.projectsPath(), &pf); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("load projects: %w", err)
	}
	return pf.Projects, nil
}

// GetProject 根据名称获取单个项目
func (s *Store) GetProject(name string) (*Project, error) {
	projects, err := s.ListProjects()
	if err != nil {
		return nil, err
	}
	for i := range projects {
		if projects[i].Name == name {
			return &projects[i], nil
		}
	}
	return nil, fmt.Errorf("project %q not found", name)
}

// SaveProject 保存或更新项目
func (s *Store) SaveProject(p *Project) error {
	projects, err := s.ListProjects()
	if err != nil {
		return err
	}

	found := false
	for i := range projects {
		if projects[i].Name == p.Name {
			projects[i] = *p
			found = true
			break
		}
	}
	if !found {
		projects = append(projects, *p)
	}

	return s.saveYAML(s.projectsPath(), &projectsFile{Projects: projects})
}

// RemoveProject 根据名称删除项目
func (s *Store) RemoveProject(name string) error {
	projects, err := s.ListProjects()
	if err != nil {
		return err
	}

	var filtered []Project
	for _, p := range projects {
		if p.Name != name {
			filtered = append(filtered, p)
		}
	}

	if len(filtered) == len(projects) {
		return fmt.Errorf("project %q not found", name)
	}

	return s.saveYAML(s.projectsPath(), &projectsFile{Projects: filtered})
}

func (s *Store) logsDir(projectName string) string {
	return filepath.Join(s.baseDir, "logs", projectName)
}

func (s *Store) logPath(projectName string, buildID int) string {
	return filepath.Join(s.logsDir(projectName), fmt.Sprintf("%d.log", buildID))
}

// SaveLog 将构建日志保存到文件
func (s *Store) SaveLog(projectName string, buildID int, content io.Reader) error {
	dir := s.logsDir(projectName)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create logs dir: %w", err)
	}

	f, err := os.Create(s.logPath(projectName, buildID))
	if err != nil {
		return fmt.Errorf("create log file: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(f, content); err != nil {
		return fmt.Errorf("write log: %w", err)
	}
	return nil
}

// GetLog 读取构建日志内容
func (s *Store) GetLog(projectName string, buildID int) (string, error) {
	data, err := os.ReadFile(s.logPath(projectName, buildID))
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("log not found for project %q build %d", projectName, buildID)
		}
		return "", fmt.Errorf("read log: %w", err)
	}
	return string(data), nil
}

// ListLogs 列出项目的所有构建日志 ID 列表
func (s *Store) ListLogs(projectName string) ([]int, error) {
	dir := s.logsDir(projectName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read logs dir: %w", err)
	}

	var ids []int
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".log") {
			continue
		}
		idStr := strings.TrimSuffix(name, ".log")
		id, err := strconv.Atoi(idStr)
		if err != nil {
			continue
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// CleanLogs 清空指定项目的所有构建日志文件，保留 BuildID 和项目注册信息
func (s *Store) CleanLogs(projectName string) (int, error) {
	dir := s.logsDir(projectName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("read logs dir: %w", err)
	}

	count := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if err := os.Remove(path); err != nil {
			return count, fmt.Errorf("remove %s: %w", entry.Name(), err)
		}
		count++
	}
	return count, nil
}

// CleanAllLogs 清空所有项目的构建日志
func (s *Store) CleanAllLogs() (int, error) {
	logsBase := filepath.Join(s.baseDir, "logs")
	entries, err := os.ReadDir(logsBase)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("read logs base dir: %w", err)
	}

	total := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		// 删除整个项目日志目录，后续 SaveLog 会自动重建
		dir := filepath.Join(logsBase, entry.Name())
		n, err := countLogFiles(dir)
		if err != nil {
			return total, err
		}
		if err := os.RemoveAll(dir); err != nil {
			return total, fmt.Errorf("remove logs for %s: %w", entry.Name(), err)
		}
		total += n
	}
	return total, nil
}

func countLogFiles(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	count := 0
	for _, e := range entries {
		if !e.IsDir() {
			count++
		}
	}
	return count, nil
}

// NextBuildID 获取下一个构建编号，原子地递增并持久化
func (s *Store) NextBuildID(name string) (int, error) {
	p, err := s.GetProject(name)
	if err != nil {
		return 0, err
	}
	p.BuildID++
	newID := p.BuildID
	if err := s.SaveProject(p); err != nil {
		return 0, err
	}
	return newID, nil
}

package store

// Project 代表一个项目配置
type Project struct {
	Name       string `yaml:"name"`
	Path       string `yaml:"path"`
	Image      string `yaml:"image"`
	BuildID    int    `yaml:"buildid"`
	LastStatus string `yaml:"laststatus"`
	LastBuild  string `yaml:"lastbuild"`
	CreatedAt  string `yaml:"createdat"`
}

// BuildRecord 代表一条构建记录
type BuildRecord struct {
	ProjectName string `yaml:"projectname"`
	BuildID     int    `yaml:"buildid"`
	Status      string `yaml:"status"`
	StartTime   string `yaml:"starttime"`
	EndTime     string `yaml:"endtime"`
}

// projectsFile 是 projects.yaml 文件的顶层结构
type projectsFile struct {
	Projects []Project `yaml:"projects"`
}

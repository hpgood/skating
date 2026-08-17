package plugin

import "fmt"

// Plugin 定义了插件需要实现的接口
type Plugin interface {
	Name() string
	Version() string
	Init() error
	Run(ctx *Context) error
}

// Context 包含构建上下文信息，传递给插件的 Run 方法
type Context struct {
	ProjectName string
	BuildID     int
	Status      string
	Duration    string
	Output      string
}

var registry = make(map[string]Plugin)

func RegisterPlugin(p Plugin) {
	registry[p.Name()] = p
}

func GetPlugins() []Plugin {
	plugins := make([]Plugin, 0, len(registry))
	for _, p := range registry {
		plugins = append(plugins, p)
	}
	return plugins
}

func RunPlugins(ctx *Context) []error {
	var errs []error
	for _, p := range registry {
		if err := p.Run(ctx); err != nil {
			errs = append(errs, fmt.Errorf("plugin %s: %w", p.Name(), err))
		}
	}
	return errs
}
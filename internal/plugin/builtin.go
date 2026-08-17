package plugin

import "fmt"

// ConsoleNotifier 内置插件，在构建完成时打印结果摘要
type ConsoleNotifier struct{}

func (c *ConsoleNotifier) Name() string    { return "console-notifier" }
func (c *ConsoleNotifier) Version() string { return "1.0.0" }
func (c *ConsoleNotifier) Init() error     { return nil }
func (c *ConsoleNotifier) Run(ctx *Context) error {
	fmt.Printf("[plugin: console-notifier] 构建完成: %s #%d => %s\n",
		ctx.ProjectName, ctx.BuildID, ctx.Status)
	return nil
}

func init() {
	RegisterPlugin(&ConsoleNotifier{})
}
package configitem

//go:generate gomodifytags -file $GOFILE -add-tags mapstructure -remove-tags json,yaml,default -transform pascalcase -all -w --override --sort --quiet

import (
	"time"
)

// Application 应用基础配置
type Application struct {
	Name            string        `mapstructure:"Name" validate:"required"`                     // 应用名称
	Mode            string        `mapstructure:"Mode" validate:"required,oneof=dev test prod"` // 运行模式
	Version         string        `mapstructure:"Version" validate:"required"`                  // 应用版本
	ShutdownTimeout time.Duration `mapstructure:"ShutdownTimeout"`                              // 优雅关闭超时，零值时使用默认10s
}

// Flags 返回应用基础配置的命令行元数据。
func (Application) Flags(prefix string) []FlagSpec {
	return []FlagSpec{
		{Name: flagName(prefix, "Name"), Default: "", Usage: "应用名称"},
		{Name: flagName(prefix, "Mode"), Aliases: []string{"Mode"}, Default: "dev", Usage: "运行模式"},
		{Name: flagName(prefix, "Version"), Default: "v0.1.0", Usage: "应用版本"},
		{Name: flagName(prefix, "ShutdownTimeout"), Default: 10 * time.Second, Usage: "优雅关闭超时"},
	}
}

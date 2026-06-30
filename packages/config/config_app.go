package config

//go:generate gomodifytags -file $GOFILE -add-tags mapstructure -remove-tags json,yaml,default -transform pascalcase -all -w --override --sort --quiet

import "time"

// Application 应用基础配置
type Application struct {
	Name            string        `mapstructure:"Name" validate:"required"`                     // 应用名称
	Mode            string        `mapstructure:"Mode" validate:"required,oneof=dev test prod"` // 运行模式
	Version         string        `mapstructure:"Version" validate:"required"`                  // 应用版本
	ShutdownTimeout time.Duration `mapstructure:"ShutdownTimeout"`                              // 优雅关闭超时，零值时使用默认10s
}

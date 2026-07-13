package configitem

import "github.com/wplbyx/modular/packages/config"

//go:generate gomodifytags -file $GOFILE -add-tags mapstructure -remove-tags json,yaml,default -transform pascalcase -all -w --override --sort --quiet

// Logging 日志配置
type Logging struct {
	Level  string     `mapstructure:"Level" validate:"required,oneof=debug info warn error"` // 日志级别
	Output []string   `mapstructure:"Output"`                                                // 输出目标
	File   FileConfig `mapstructure:"File"`
	OTel   OTelConfig `mapstructure:"OTel"`
}

// FileConfig 是文件输出的配置
type FileConfig struct {
	Filename   string `mapstructure:"Filename"`   // 文件名 (当 output 为 file 时)
	MaxSize    int    `mapstructure:"MaxSize"`    // 单个日志文件最大大小
	MaxBackups int    `mapstructure:"MaxBackups"` // 保留的旧日志文件数量
	MaxAge     int    `mapstructure:"MaxAge"`     // 保留日志文件的最大天数
	Compress   bool   `mapstructure:"Compress"`   // 是否压缩/归档旧日志文件
	SplitRange string `mapstructure:"SplitRange"` // 日志分片逻辑：每天
}

// OTelConfig 是 OpenTelemetry 的配置
type OTelConfig struct {
	Endpoint string `mapstructure:"Endpoint"`
	Insecure bool   `mapstructure:"Insecure"`
}

// Flags 返回日志配置的命令行元数据。
func (Logging) Flags(prefix string) []config.FlagSpec {
	return []config.FlagSpec{
		{Name: prefix + ".level", Default: "info", Usage: "日志级别"},
		{Name: prefix + ".output", Default: []string{"console"}, Usage: "日志输出目标"},
		{Name: prefix + ".file.filename", Default: "./logs/app.log", Usage: "日志文件名"},
		{Name: prefix + ".file.maxsize", Default: 100, Usage: "单个日志文件最大大小"},
		{Name: prefix + ".file.maxbackups", Default: 7, Usage: "保留的旧日志文件数量"},
		{Name: prefix + ".file.maxage", Default: 15, Usage: "保留日志文件的最大天数"},
		{Name: prefix + ".file.compress", Default: true, Usage: "是否压缩旧日志文件"},
		{Name: prefix + ".file.splitrange", Default: "daily", Usage: "日志分片逻辑"},
		{Name: prefix + ".otel.endpoint", Default: "", Usage: "日志OTel Endpoint"},
		{Name: prefix + ".otel.insecure", Default: true, Usage: "日志OTel是否使用非TLS连接"},
	}
}

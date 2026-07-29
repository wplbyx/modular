package configitem

import (
	"time"
)

//go:generate gomodifytags -file $GOFILE -add-tags mapstructure -remove-tags json,yaml,default -transform pascalcase -all -w --override --sort --quiet

// Logging 日志配置
type Logging struct {
	Level  string             `mapstructure:"Level" validate:"required,oneof=debug info warn error"` // 日志级别
	Output []string           `mapstructure:"Output"`                                                // 输出目标
	Async  AsyncLoggingConfig `mapstructure:"Async"`
	File   FileConfig         `mapstructure:"File"`
	OTel   OTelConfig         `mapstructure:"OTel"`
}

// AsyncLoggingConfig controls the RingMPSC-backed dispatcher.
type AsyncLoggingConfig struct {
	Enabled      bool          `mapstructure:"Enabled"`
	Capacity     int           `mapstructure:"Capacity" validate:"omitempty,min=1"`
	ErrorTimeout time.Duration `mapstructure:"ErrorTimeout"`
	FlushTimeout time.Duration `mapstructure:"FlushTimeout"`
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
func (Logging) Flags(prefix string) []FlagSpec {
	return []FlagSpec{
		{Name: flagName(prefix, "Level"), Default: "info", Usage: "日志级别"},
		{Name: flagName(prefix, "Output"), Default: []string{"console"}, Usage: "日志输出目标"},
		{Name: flagName(prefix, "Async.Enabled"), Default: true, Usage: "enable asynchronous logging"},
		{Name: flagName(prefix, "Async.Capacity"), Default: 8192, Usage: "asynchronous log queue capacity"},
		{Name: flagName(prefix, "Async.ErrorTimeout"), Default: 50 * time.Millisecond, Usage: "maximum enqueue wait for error logs"},
		{Name: flagName(prefix, "Async.FlushTimeout"), Default: 5 * time.Second, Usage: "default log flush timeout"},
		{Name: flagName(prefix, "File.Filename"), Default: "./logs/app.log", Usage: "日志文件名"},
		{Name: flagName(prefix, "File.MaxSize"), Default: 100, Usage: "单个日志文件最大大小"},
		{Name: flagName(prefix, "File.MaxBackups"), Default: 7, Usage: "保留的旧日志文件数量"},
		{Name: flagName(prefix, "File.MaxAge"), Default: 15, Usage: "保留日志文件的最大天数"},
		{Name: flagName(prefix, "File.Compress"), Default: true, Usage: "是否压缩旧日志文件"},
		{Name: flagName(prefix, "File.SplitRange"), Default: "daily", Usage: "日志分片逻辑"},
		{Name: flagName(prefix, "OTel.Endpoint"), Default: "", Usage: "日志OTel Endpoint"},
		{Name: flagName(prefix, "OTel.Insecure"), Default: true, Usage: "日志OTel是否使用非TLS连接"},
	}
}

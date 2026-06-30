package config

//go:generate gomodifytags -file $GOFILE -add-tags mapstructure -remove-tags json,yaml,default -transform pascalcase -all -w --override --sort --quiet

// Telemetry 遥测
type Telemetry struct {
	Logger string `mapstructure:"Logger"` // 日志输出
	Metric string `mapstructure:"Metric"` // 指标输出
	Tracer string `mapstructure:"Tracer"` // 链路输出
}

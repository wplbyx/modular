package config

import (
	"time"
)

//go:generate gomodifytags -file $GOFILE -add-tags mapstructure -remove-tags json,yaml,default -transform pascalcase -all -w --override --sort --quiet

// HTTP HTTP 服务器配置
type HTTP struct {
	Host              string        `mapstructure:"Host" validate:"required"`                    // 监听主机
	Port              int           `mapstructure:"Port" validate:"required,min=1000,max=65535"` // HTTP服务端口
	ReadHeaderTimeout time.Duration `mapstructure:"ReadHeaderTimeout"`                           // 读取请求头超时 default:"5s"
	ReadTimeout       time.Duration `mapstructure:"ReadTimeout"`                                 // 读取请求体超时 default:"30s"
	WriteTimeout      time.Duration `mapstructure:"WriteTimeout"`                                // 写入响应超时 default:"30s"
	IdleTimeout       time.Duration `mapstructure:"IdleTimeout"`                                 // 空闲超时    default:"120s"
	ShutdownTimeout   time.Duration `mapstructure:"ShutdownTimeout"`                             // 优雅关闭超时
	EnableTLS         bool          `mapstructure:"EnableTLS"`                                   // 是否启用TLS
	TLSKeyFile        string        `mapstructure:"TLSKeyFile"`                                  // TLS私钥文件路径
	TLSCertFile       string        `mapstructure:"TLSCertFile"`                                 // TLS证书文件路径
}

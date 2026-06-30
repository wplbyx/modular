package config

import (
	"time"
)

//go:generate gomodifytags -file $GOFILE -add-tags mapstructure -remove-tags json,yaml,default -transform pascalcase -all -w --override --sort --quiet

// GRPC gRPC 服务器配置
type GRPC struct {
	Host            string        `mapstructure:"Host" validate:"required"`                    // 监听主机
	Port            int           `mapstructure:"Port" validate:"required,min=1000,max=65535"` // gRPC服务端口
	Timeout         time.Duration `mapstructure:"Timeout"`                                     // RPC调用超时 default:"30s"
	ShutdownTimeout time.Duration `mapstructure:"ShutdownTimeout"`                             // 优雅关闭超时 default:"30s"
	EnableTLS       bool          `mapstructure:"EnableTLS"`                                   // 是否启用TLS
	TLSKeyFile      string        `mapstructure:"TLSKeyFile"`                                  // TLS私钥文件路径
	TLSCertFile     string        `mapstructure:"TLSCertFile"`                                 // TLS证书文件路径
}

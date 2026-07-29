package configitem

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

// Flags 返回 gRPC 配置的命令行元数据。
func (GRPC) Flags(prefix string) []FlagSpec {
	return []FlagSpec{
		{Name: flagName(prefix, "Host"), Default: "0.0.0.0", Usage: "gRPC监听主机"},
		{Name: flagName(prefix, "Port"), Default: 19000, Usage: "gRPC服务端口"},
		{Name: flagName(prefix, "Timeout"), Default: 30 * time.Second, Usage: "RPC调用超时"},
		{Name: flagName(prefix, "ShutdownTimeout"), Default: 30 * time.Second, Usage: "gRPC优雅关闭超时"},
		{Name: flagName(prefix, "EnableTLS"), Default: false, Usage: "是否启用gRPC TLS"},
		{Name: flagName(prefix, "TLSKeyFile"), Default: "", Usage: "gRPC TLS私钥文件路径"},
		{Name: flagName(prefix, "TLSCertFile"), Default: "", Usage: "gRPC TLS证书文件路径"},
	}
}

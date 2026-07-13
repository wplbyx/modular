package configitem

import (
	"time"

	"github.com/wplbyx/modular/packages/config"
)

//go:generate gomodifytags -file $GOFILE -add-tags mapstructure -remove-tags json,yaml,default -transform pascalcase -all -w --override --sort --quiet

// Database 数据库配置
type Database struct {
	Dsn             string        `mapstructure:"Dsn" validate:"required,oneof=sqlite mysql postgres clickhouse mongodb"` // 数据库驱动
	Urls            []string      `mapstructure:"Urls"`                                                                   // 数据库连接URL列表
	Host            string        `mapstructure:"Host"`                                                                   // 数据库主机
	Port            int           `mapstructure:"Port" validate:"max=65535"`                                              // 数据库端口
	Path            string        `mapstructure:"Path"`                                                                   // 数据库路径(sqlite)
	Database        string        `mapstructure:"Database"`                                                               // 数据库名
	Username        string        `mapstructure:"Username"`                                                               // 用户名
	Password        string        `mapstructure:"Password"`                                                               // 密码
	MaxOpenConn     int           `mapstructure:"MaxOpenConn"`                                                            // 连接池最大连接数 default:"25
	MaxIdleConn     int           `mapstructure:"MaxIdleConn"`                                                            // 连接池最大空闲连接数 default:"5
	MaxPoolSize     int           `mapstructure:"MaxPoolSize"`                                                            // MongoDB 连接池最大连接数
	ReplicaSet      string        `mapstructure:"ReplicaSet"`                                                             // MongoDB 副本集名称
	ConnMaxLifetime time.Duration `mapstructure:"ConnMaxLifetime"`                                                        // 连接最大存活时间 default:"1h
	ConnMaxIdleTime time.Duration `mapstructure:"ConnMaxIdleTime"`                                                        // 连接最大存活时间 default:"1h
	EnableTLS       bool          `mapstructure:"EnableTLS"`                                                              // 是否启用TLS
}

// Flags 返回数据库配置的命令行元数据。
func (Database) Flags(prefix string) []config.FlagSpec {
	return []config.FlagSpec{
		{Name: prefix + ".dsn", Default: "", Usage: "数据库驱动"},
		{Name: prefix + ".urls", Default: []string(nil), Usage: "数据库连接URL列表"},
		{Name: prefix + ".host", Default: "", Usage: "数据库主机"},
		{Name: prefix + ".port", Default: 0, Usage: "数据库端口"},
		{Name: prefix + ".path", Default: "", Usage: "数据库路径"},
		{Name: prefix + ".database", Default: "", Usage: "数据库名"},
		{Name: prefix + ".username", Default: "", Usage: "数据库用户名"},
		{Name: prefix + ".password", Default: "", Usage: "数据库密码"},
		{Name: prefix + ".maxopenconn", Default: 25, Usage: "连接池最大连接数"},
		{Name: prefix + ".maxidleconn", Default: 5, Usage: "连接池最大空闲连接数"},
		{Name: prefix + ".maxpoolsize", Default: 0, Usage: "MongoDB连接池最大连接数"},
		{Name: prefix + ".replicaset", Default: "", Usage: "MongoDB副本集名称"},
		{Name: prefix + ".connmaxlifetime", Default: time.Hour, Usage: "连接最大存活时间"},
		{Name: prefix + ".connmaxidletime", Default: time.Hour, Usage: "连接最大空闲时间"},
		{Name: prefix + ".enabletls", Default: false, Usage: "是否启用数据库TLS"},
	}
}

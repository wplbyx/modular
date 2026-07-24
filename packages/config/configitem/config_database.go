package configitem

import (
	"time"

	"github.com/wplbyx/modular/packages/config"
)

//go:generate gomodifytags -file $GOFILE -add-tags mapstructure -remove-tags json,yaml,default -transform pascalcase -all -w --override --sort --quiet

// Database 是 SQL 数据库连接池配置。数据库方言由应用装配层选择。
type Database struct {
	DSN             string        `mapstructure:"DSN" validate:"required"`
	MaxOpenConn     int           `mapstructure:"MaxOpenConn"`
	MaxIdleConn     int           `mapstructure:"MaxIdleConn"`
	ConnMaxLifetime time.Duration `mapstructure:"ConnMaxLifetime"`
	ConnMaxIdleTime time.Duration `mapstructure:"ConnMaxIdleTime"`
}

// Flags 返回 SQL 数据库配置的命令行元数据。
func (Database) Flags(prefix string) []config.FlagSpec {
	return []config.FlagSpec{
		{Name: prefix + ".dsn", Default: "", Usage: "数据库连接字符串"},
		{Name: prefix + ".maxopenconn", Default: 25, Usage: "连接池最大连接数"},
		{Name: prefix + ".maxidleconn", Default: 5, Usage: "连接池最大空闲连接数"},
		{Name: prefix + ".connmaxlifetime", Default: time.Hour, Usage: "连接最大存活时间"},
		{Name: prefix + ".connmaxidletime", Default: time.Hour, Usage: "连接最大空闲时间"},
	}
}

// Mongo 是 MongoDB 客户端配置，与 SQL 数据库配置分离。
type Mongo struct {
	URI         string   `mapstructure:"URI"`
	Hosts       []string `mapstructure:"Hosts"`
	Database    string   `mapstructure:"Database"`
	Username    string   `mapstructure:"Username"`
	Password    string   `mapstructure:"Password"`
	MaxPoolSize int      `mapstructure:"MaxPoolSize" validate:"min=0"`
	ReplicaSet  string   `mapstructure:"ReplicaSet"`
}

// Flags 返回 MongoDB 配置的命令行元数据。
func (Mongo) Flags(prefix string) []config.FlagSpec {
	return []config.FlagSpec{
		{Name: prefix + ".uri", Default: "", Usage: "MongoDB 连接 URI"},
		{Name: prefix + ".hosts", Default: []string(nil), Usage: "MongoDB 主机列表"},
		{Name: prefix + ".database", Default: "", Usage: "MongoDB 数据库名"},
		{Name: prefix + ".username", Default: "", Usage: "MongoDB 用户名"},
		{Name: prefix + ".password", Default: "", Usage: "MongoDB 密码"},
		{Name: prefix + ".maxpoolsize", Default: 0, Usage: "MongoDB 最大连接池大小"},
		{Name: prefix + ".replicaset", Default: "", Usage: "MongoDB 副本集名称"},
	}
}

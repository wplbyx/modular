package config

import (
	"time"
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

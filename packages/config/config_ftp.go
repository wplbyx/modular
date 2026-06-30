package config

//go:generate gomodifytags -file $GOFILE -add-tags mapstructure -remove-tags json,yaml,default -transform pascalcase -all -w --override --sort --quiet

// Ftp FTP服务器配置
type Ftp struct {
	Host     string `mapstructure:"Host" validate:"required"` // FTP服务器地址
	Port     int    `mapstructure:"Port" validate:"required"` // FTP服务器端口
	Username string `mapstructure:"Username"`                 // 用户名
	Password string `mapstructure:"Password"`                 // 密码
	Domain   string `mapstructure:"Domain"`                   // 访问域名
	Prefix   string `mapstructure:"Prefix"`                   // 文件路径前缀
}

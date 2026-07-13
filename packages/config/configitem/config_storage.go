package configitem

import (
	"time"

	"github.com/wplbyx/modular/packages/config"
)

//go:generate gomodifytags -file $GOFILE -add-tags mapstructure -remove-tags json,yaml,default -transform pascalcase -all -w --override --sort --quiet

// Storage 存储
type Storage struct {
	Type          string             `mapstructure:"Type" validate:"required,oneof=disk oss"` // 存储类型
	PublicBaseURL string             `mapstructure:"PublicBaseURL"`                           // 文件对外访问域名
	Disk          *DiskStorageConfig `mapstructure:"Disk"`                                    // 本地磁盘存储配置
	OSS           *OSSStorageConfig  `mapstructure:"OSS"`                                     // 阿里云 OSS 对象存储配置
}

// DiskStorageConfig 本地磁盘存储配置
type DiskStorageConfig struct {
	RootDir string `mapstructure:"RootDir"` // 存储根目录（绝对路径，跨平台）
	BaseUrl string `mapstructure:"BaseUrl"` // 访问域名（用于 GetUrl：baseUrl + key）
}

// OSSStorageConfig 阿里云 OSS 对象存储配置
type OSSStorageConfig struct {
	AccessKeyID     string        `mapstructure:"AccessKeyID"`
	AccessKeySecret string        `mapstructure:"AccessKeySecret"`
	SecurityToken   string        `mapstructure:"SecurityToken"`
	Region          string        `mapstructure:"Region"`
	Bucket          string        `mapstructure:"Bucket"`
	Endpoint        string        `mapstructure:"Endpoint"`
	BaseDir         string        `mapstructure:"BaseDir"` // 对象 key 前缀
	DisableSSL      bool          `mapstructure:"DisableSSL"`
	UseCName        bool          `mapstructure:"UseCName"`
	Timeout         time.Duration `mapstructure:"Timeout"`
	MaxRetries      int           `mapstructure:"MaxRetries"`
}

// Flags 返回存储配置的命令行元数据。
func (Storage) Flags(prefix string) []config.FlagSpec {
	return []config.FlagSpec{
		{Name: prefix + ".type", Default: "disk", Usage: "存储类型"},
		{Name: prefix + ".publicbaseurl", Default: "", Usage: "文件对外访问域名"},
		{Name: prefix + ".disk.rootdir", Default: "./storage/upload", Usage: "本地磁盘存储根目录"},
		{Name: prefix + ".disk.baseurl", Default: "", Usage: "本地磁盘访问域名"},

		{Name: prefix + ".oss.accesskeyid", Default: "", Usage: "OSS AccessKey ID"},
		{Name: prefix + ".oss.accesskeysecret", Default: "", Usage: "OSS AccessKey Secret"},
		{Name: prefix + ".oss.securitytoken", Default: "", Usage: "OSS临时安全令牌"},
		{Name: prefix + ".oss.region", Default: "", Usage: "OSS区域"},
		{Name: prefix + ".oss.bucket", Default: "", Usage: "OSS Bucket"},
		{Name: prefix + ".oss.endpoint", Default: "", Usage: "OSS Endpoint"},
		{Name: prefix + ".oss.basedir", Default: "", Usage: "OSS对象 key 前缀"},
		{Name: prefix + ".oss.disablessl", Default: false, Usage: "是否禁用OSS SSL"},
		{Name: prefix + ".oss.usecname", Default: false, Usage: "是否使用OSS CNAME"},
		{Name: prefix + ".oss.timeout", Default: 60 * time.Second, Usage: "OSS请求超时"},
		{Name: prefix + ".oss.maxretries", Default: 3, Usage: "OSS最大重试次数"},
	}
}

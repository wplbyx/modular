package aliyun_oss

import (
	"fmt"

	"github.com/spf13/pflag"
)

// LocalDiskStorage 本地磁盘存储配置
type LocalDiskStorage struct {
	RootDir string `mapstructure:"ROOTDIR"` // 存储根目录（绝对路径，跨平台）
	BaseUrl string `mapstructure:"BASEURL"` // 访问域名（用于 GetUsefulUrl：baseUrl + key）
}

func NewLocalDiskStorageOptions() *LocalDiskStorage {
	return &LocalDiskStorage{}
}

func (o *LocalDiskStorage) Validate() []error {
	var errs []error
	if o.RootDir == "" {
		errs = append(errs, fmt.Errorf("options ->> LocalDiskStorage.RootDir must be specified"))
	}
	if o.BaseUrl == "" {
		errs = append(errs, fmt.Errorf("options ->> LocalDiskStorage.BaseUrl must be specified"))
	}
	return errs
}

func (o *LocalDiskStorage) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&o.RootDir, "disk.root_dir", o.RootDir, "local disk storage root directory")
	fs.StringVar(&o.BaseUrl, "disk.base_url", o.BaseUrl, "base url for generating accessible file url")
}

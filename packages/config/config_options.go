package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	_ "github.com/spf13/viper/remote"
)

type configFileSource struct {
	path           string
	ignoreNotFound bool
	filesystem     fs.FS
}

type remoteConfigSource struct {
	provider string
	endpoint string
	path     string
	format   string
}

// ConfigureLoaderOption 定义配置选项的函数类型
type ConfigureLoaderOption func(*ConfigureLoader) error

// WithCommand 根据 FlagSpec 将自动注册的 Cobra flags 绑定到对应的 Viper 配置键。
//
// 每个 FlagSpec 先绑定规范参数，再检查 aliases。如果规范参数被显式设置，所有 aliases
// 都会被忽略；否则显式设置的 alias 会绑定到 spec.Name。多个 alias 同时设置时，
// Aliases 列表中最后一个已设置项生效，与原有绑定顺序保持一致。
func WithCommand(cmd *cobra.Command, specs []FlagSpec) ConfigureLoaderOption {
	return func(loader *ConfigureLoader) error {
		for _, spec := range specs {
			// 规范参数与 Viper 配置键同名；绑定是惰性的，Viper 读取时查询 flag 当前值。
			targetFlag := cmd.Flags().Lookup(spec.Name)
			if targetFlag == nil {
				continue
			}
			if err := loader.v.BindPFlag(spec.Name, targetFlag); err != nil {
				return err
			}

			// 规范参数优先级最高；用户已显式设置时不再考虑任何 alias。
			if targetFlag.Changed {
				continue
			}

			// alias 只在被显式设置时接管规范配置键；后续已设置 alias 会覆盖前一个绑定。
			for _, alias := range spec.Aliases {
				aliasFlag := cmd.Flags().Lookup(alias)
				if aliasFlag == nil || !aliasFlag.Changed {
					continue
				}
				if err := loader.v.BindPFlag(spec.Name, aliasFlag); err != nil {
					return err
				}
			}
		}
		return nil
	}
}

// WithConfigFile 设置需要精确读取的配置文件路径。
// ignoreNotFound 为 true 时仅忽略文件不存在错误，格式、权限等其它错误仍会返回。
func WithConfigFile(path string, ignoreNotFound bool) ConfigureLoaderOption {
	return func(c *ConfigureLoader) error {
		if c.fileSource != nil {
			return errors.New("config file source already configured")
		}
		if strings.TrimSpace(path) == "" {
			return errors.New("config file path is empty")
		}

		c.fileSource = &configFileSource{path: path, ignoreNotFound: ignoreNotFound}
		return nil
	}
}

// WithConfigFS 从 fs.FS 读取配置，适用于 go:embed 和测试内存文件系统。
func WithConfigFS(filesystem fs.FS, path string) ConfigureLoaderOption {
	return func(loader *ConfigureLoader) error {
		if loader.fileSource != nil {
			return errors.New("config file source already configured")
		}
		if filesystem == nil {
			return errors.New("config filesystem is nil")
		}
		if strings.TrimSpace(path) == "" {
			return errors.New("config file path is empty")
		}
		format := configFormatFromFile(path)
		if format == "" || !isSupportedConfigFormat(format) {
			return fmt.Errorf("unsupported config file format %q", format)
		}
		loader.fileSource = &configFileSource{path: path, filesystem: filesystem}
		return nil
	}
}

// WithEnvPrefix 设置环境变量前缀，例如 MYAPP_
func WithEnvPrefix(prefix string, replaces ...*strings.Replacer) ConfigureLoaderOption {
	return func(c *ConfigureLoader) error {
		// 设置读取环境变量相关配置
		c.v.SetEnvPrefix(prefix)
		if len(replaces) > 0 {
			c.v.SetEnvKeyReplacer(replaces[0])
		}
		// 显式绑定已存在的环境变量，确保 Unmarshal 能识别，同时保留 Viper 的优先级语义。
		for _, environ := range os.Environ() {
			parts := strings.SplitN(environ, "=", 2)
			if len(parts) != 2 {
				continue
			}
			envKey := strings.TrimSpace(parts[0])
			key := envKey

			// 有前缀
			if prefix != "" {
				if !strings.HasPrefix(key, prefix+"_") {
					continue
				}
				key = strings.TrimPrefix(key, prefix+"_")
			}

			viperKey := strings.ReplaceAll(strings.ToLower(key), "_", ".")
			if err := c.v.BindEnv(viperKey, envKey); err != nil {
				return err
			}
		}
		return nil
	}
}

// WithRemoteProvider 设置远程配置中心。
// provider 是 Viper 支持的 provider 名称，例如 etcd3、consul 或 firestore；
// endpoint 是 provider 原生地址；path 是远程 KV key。远程内容格式优先从 path
// 扩展名推断，无法推断时默认使用 yaml。需要显式格式时应使用 WithRemoteURL。
func WithRemoteProvider(provider, endpoint, path string) ConfigureLoaderOption {
	return func(c *ConfigureLoader) error {
		return c.setRemoteConfigSource(remoteConfigSource{
			provider: strings.ToLower(strings.TrimSpace(provider)),
			endpoint: strings.TrimSpace(endpoint),
			path:     path,
			format:   configFormatFromPath(path),
		})
	}
}

// WithRemoteURL 使用统一 URL 描述远程配置中心。
// 支持 etcd://host/key 和 consul://host/key；etcd 默认映射到 etcd v3。
// URL 查询参数 format 可显式指定远程内容格式，未指定时依次使用 key 扩展名和 yaml。
func WithRemoteURL(rawURL string) ConfigureLoaderOption {
	return func(c *ConfigureLoader) error {
		source, err := parseRemoteConfigURL(rawURL)
		if err != nil {
			return err
		}
		return c.setRemoteConfigSource(source)
	}
}

func (c *ConfigureLoader) setRemoteConfigSource(source remoteConfigSource) error {
	if c.remoteSource != nil {
		return errors.New("remote config source already configured")
	}
	if !slices.Contains(viper.SupportedRemoteProviders, source.provider) {
		return fmt.Errorf("unsupported remote config provider %q", source.provider)
	}
	if source.endpoint == "" {
		return errors.New("remote config endpoint is empty")
	}
	trimmedPath := strings.TrimSpace(source.path)
	if trimmedPath == "" || trimmedPath == "/" {
		return errors.New("remote config path is empty")
	}

	source.format = normalizeConfigFormat(source.format)
	if source.format == "" {
		source.format = "yaml"
	}
	if !isSupportedConfigFormat(source.format) {
		return fmt.Errorf("unsupported remote config format %q", source.format)
	}

	c.remoteSource = &source
	return nil
}

// loadConfigSources 在所有 ConfigureLoaderOption 应用完成后加载配置源。
// 本地文件与远程 KV 分别进入 Viper 的 config 和 kvstore 层，因此即使先读取本地文件，
// 最终优先级仍然是本地文件高于远程配置。远程读取失败时，只有已经成功解析的本地文件
// 才能作为兜底；被 ignoreNotFound 忽略的缺失文件不属于有效兜底。
func (c *ConfigureLoader) loadConfigSources() error {
	localLoaded, localFormat, err := c.loadConfigFile()
	if err != nil {
		return err
	}
	if c.remoteSource == nil {
		return nil
	}

	remote := c.remoteSource
	if localLoaded && localFormat != remote.format {
		log.Printf(
			"config: skip remote %s:%s because format %q differs from local file %q format %q",
			remote.provider,
			remote.path,
			remote.format,
			c.fileSource.path,
			localFormat,
		)
		return nil
	}

	if err := c.v.AddRemoteProvider(remote.provider, remote.endpoint, remote.path); err != nil {
		return fmt.Errorf("add remote config provider %q: %w", remote.provider, err)
	}
	c.remoteReady = true
	c.v.SetConfigType(remote.format)
	if err := c.v.ReadRemoteConfig(); err != nil {
		wrapped := fmt.Errorf("read remote config %s:%s: %w", remote.provider, remote.path, err)
		if localLoaded {
			log.Printf("config: %v; using local file %q", wrapped, c.fileSource.path)
			return nil
		}
		return wrapped
	}

	return nil
}

func (c *ConfigureLoader) loadConfigFile() (bool, string, error) {
	if c.fileSource == nil {
		return false, "", nil
	}

	source := c.fileSource
	if source.filesystem != nil {
		content, err := fs.ReadFile(source.filesystem, source.path)
		if err != nil {
			return false, "", fmt.Errorf("read config file %q from fs.FS: %w", source.path, err)
		}
		format := configFormatFromFile(source.path)
		c.v.SetConfigType(format)
		if err := c.v.ReadConfig(bytes.NewReader(content)); err != nil {
			return false, "", fmt.Errorf("parse config file %q from fs.FS: %w", source.path, err)
		}
		return true, format, nil
	}

	c.v.SetConfigFile(source.path)
	if err := c.v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if source.ignoreNotFound && (errors.Is(err, os.ErrNotExist) || errors.As(err, &notFound)) {
			return false, "", nil
		}
		return false, "", fmt.Errorf("read config file %q: %w", source.path, err)
	}

	return true, configFormatFromFile(source.path), nil
}

func parseRemoteConfigURL(rawURL string) (remoteConfigSource, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return remoteConfigSource{}, fmt.Errorf("parse remote config URL: %w", err)
	}
	if parsed.User != nil {
		return remoteConfigSource{}, errors.New("remote config URL must not contain credentials")
	}
	if parsed.Fragment != "" {
		return remoteConfigSource{}, errors.New("remote config URL must not contain a fragment")
	}
	if parsed.Host == "" {
		return remoteConfigSource{}, errors.New("remote config URL host is empty")
	}
	if strings.TrimSpace(parsed.Path) == "" || parsed.Path == "/" {
		return remoteConfigSource{}, errors.New("remote config URL path is empty")
	}

	query := parsed.Query()
	for key := range query {
		if key != "format" {
			return remoteConfigSource{}, fmt.Errorf("unsupported remote config URL query parameter %q", key)
		}
	}
	if len(query["format"]) > 1 {
		return remoteConfigSource{}, errors.New("remote config URL format must be specified once")
	}

	format := query.Get("format")
	if format == "" {
		format = configFormatFromPath(parsed.Path)
	}

	source := remoteConfigSource{
		path:   parsed.Path,
		format: format,
	}
	switch strings.ToLower(parsed.Scheme) {
	case "etcd":
		source.provider = "etcd3"
		source.endpoint = "http://" + parsed.Host
	case "consul":
		source.provider = "consul"
		source.endpoint = parsed.Host
	default:
		return remoteConfigSource{}, fmt.Errorf("unsupported remote config URL scheme %q", parsed.Scheme)
	}

	return source, nil
}

func configFormatFromFile(filename string) string {
	return normalizeConfigFormat(strings.TrimPrefix(filepath.Ext(filename), "."))
}

func configFormatFromPath(configPath string) string {
	return normalizeConfigFormat(strings.TrimPrefix(path.Ext(configPath), "."))
}

func normalizeConfigFormat(format string) string {
	format = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(format, ".")))
	switch format {
	case "yml":
		return "yaml"
	case "prop", "props":
		return "properties"
	case "dotenv":
		return "env"
	default:
		return format
	}
}

func isSupportedConfigFormat(format string) bool {
	for _, supported := range viper.SupportedExts {
		if normalizeConfigFormat(supported) == format {
			return true
		}
	}
	return false
}

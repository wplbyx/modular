package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// CommandOptions 定义 Cobra 根命令的展示信息、配置来源和最终执行函数。
//
// 泛型参数 T 是业务侧组合后的完整配置类型。NewRootCommand 会扫描 T 的导出字段，
// 只为实现了 FlagProvider 的配置项注册命令行参数，并在执行 Run 前完成配置加载和校验。
type CommandOptions[T any] struct {
	Use           string                          // Use 对应 cobra.Command.Use。为空时依次回退到 Name 和当前可执行文件名。
	Name          string                          // Name 是应用名称，同时也是 Use 为空时的首选命令名。
	Short         string                          // Short 是 Cobra 帮助信息中的简短说明。
	Long          string                          // Long 是 Cobra 帮助信息中的详细说明。
	DefaultFile   string                          // DefaultFile 是 --config/-c 的默认值。默认文件不存在时允许继续使用其它配置来源。
	DefaultRemote string                          // DefaultRemote 是 --remote 的默认值，支持 etcd:// 和 consul:// URL。
	EnvPrefix     string                          // EnvPrefix 限定参与加载的环境变量前缀，例如 ORDER_HTTP_PORT 中的 ORDER。
	StrictDecode  bool                            // StrictDecode 为 true 时拒绝目标结构体中不存在的配置键。
	Args          cobra.PositionalArgs            // Args 是 Cobra 的位置参数校验函数；为空时不额外限制位置参数。
	BindFlags     func(*cobra.Command)            // BindFlags 在模块配置参数注册完成后调用，用于补充业务自定义的 Cobra 参数。
	Run           func(context.Context, *T) error // Run 在配置文件、环境变量和命令行参数合并并通过校验后执行。
}

// NewRootCommand 创建一个集成 Cobra、Viper 与模块化配置对象的根命令。
//
// 构建阶段会完成以下工作：
//   - 根据 CommandOptions 确定命令名称和帮助信息；
//   - 注册 --config/-c；
//   - 注册 --remote；
//   - 扫描 T 中实现 FlagProvider 的配置项，并注册对应的 persistent flags；
//   - 调用 BindFlags，让业务补充自己的命令行参数。
//
// Execute 进入 RunE 后才会读取配置。配置值由配置文件、环境变量和 Cobra flags
// 交给统一的 ConfigureLoader 合并，然后反序列化并校验为 *T。对于同一个配置键，
// 显式规范参数优先于 alias，最终优先级为 Cobra 参数、环境变量、本地文件、远程 KV、默认值。
func NewRootCommand[T any](opts CommandOptions[T]) *cobra.Command {
	// 命令名按 Use、Name、可执行文件名的顺序回退，确保 Cobra 始终有可展示的名称。
	use := opts.Use
	if use == "" {
		use = opts.Name
	}
	if use == "" {
		use = filepath.Base(os.Args[0])
		if use == "." {
			use = "app"
		}
	}

	// 获取 FlagSpec 对象，FlagSpec是配置参数注册和 Viper 绑定的唯一元数据来源。
	specs := GetConfigFlagSpecs[T]()

	// NewRootCommand 的返回类型固定为 *cobra.Command，无法在构建阶段直接返回注册错误。
	// 因此保存 buildErr，并在 Execute 进入 RunE 时作为命令执行错误返回。
	var buildErr error
	cmd := &cobra.Command{
		Use:   use,
		Short: opts.Short,
		Long:  opts.Long,
		Args:  opts.Args,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// flag 重名、不支持的默认值类型等构建错误在真正执行命令时统一暴露。
			if buildErr != nil {
				return buildErr
			}
			if opts.Run == nil {
				return errors.New("command run function is nil")
			}

			// 配置只在 Cobra 完成参数解析后加载，此时才能准确判断哪些 flag 被显式设置，
			// 并通过 InitConfigure 统一合并文件、环境变量和命令行参数。
			cfg, err := LoadViperConfig[T](cmd, opts, specs)
			if err != nil {
				return err
			}
			return opts.Run(cmd.Context(), cfg)
		},
	}

	// 配置参数使用 persistent flag，使根命令及其子命令共享同一个配置文件入口。
	cmd.PersistentFlags().StringP("config", "c", opts.DefaultFile, "配置文件路径")
	cmd.PersistentFlags().String("remote", opts.DefaultRemote, "远程配置地址（etcd://host/key 或 consul://host/key）")

	// 模块 flags 先注册，业务 BindFlags 后执行，便于尽早发现参数名冲突。
	buildErr = RegisterConfigFlags(cmd, specs)
	if opts.BindFlags != nil {
		opts.BindFlags(cmd)
	}

	return cmd
}

// GetConfigFlagSpecs 返回配置聚合对象中所有模块声明的命令行元数据。
//
// T 应当是业务组合配置结构体或其指针。函数只扫描 T 的第一层导出字段；
// 字段对应的配置类型实现 FlagProvider 时，才会参与命令行参数注册。
// 字段的 mapstructure tag 决定配置键前缀，例如 mapstructure:"HTTP" 会生成 HTTP.Port。
func GetConfigFlagSpecs[T any]() []FlagSpec {
	typ := reflect.TypeOf((*T)(nil)).Elem()
	return getConfigFlagSpecs(typ, "", true)
}

// GetConfigFlagSpecsWithPrefix 返回配置聚合对象中所有模块声明的命令行元数据，
// 并在每个配置键前追加 parentPrefix。
//
// 该函数用于把一个业务 Config 继续组合到更高层配置对象中。例如 user.Config
// 实现 FlagProvider 时，可以在 Flags("User") 中调用本函数，最终生成
// User.HTTP.Port、User.Redis.Host 等带业务模块前缀的配置键。
func GetConfigFlagSpecsWithPrefix[T any](parentPrefix string) []FlagSpec {
	// 通过 (*T)(nil) 获取 T 的 reflect.Type，即使 T 本身是指针类型也不会实例化配置对象。
	typ := reflect.TypeOf((*T)(nil)).Elem()
	return getConfigFlagSpecs(typ, parentPrefix, false)
}

func getConfigFlagSpecs(typ reflect.Type, parentPrefix string, useRootProvider bool) []FlagSpec {
	if typ == nil {
		return nil
	}
	if useRootProvider {
		provider, ok := flagProvider(typ)
		if !ok {
			return getConfigFlagSpecs(typ, parentPrefix, false)
		}
		return provider.Flags(parentPrefix)
	}

	// 兼容 T、*T、**T 等形式，最终只处理底层结构体。
	for typ.Kind() == reflect.Ptr {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct {
		return nil
	}

	specs := make([]FlagSpec, 0, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		// PkgPath 非空表示未导出字段；业务外部无法正常配置，因而不参与扫描。
		field := typ.Field(i)
		if field.PkgPath != "" {
			continue
		}

		// mapstructure:"-" 明确表示该字段不属于配置输入。
		fieldPrefix := configPrefix(field)
		if fieldPrefix == "-" {
			continue
		}

		// 普通业务字段可以存在于聚合结构体中，但只有配置模块才需要自动暴露 flags。
		provider, ok := flagProvider(field.Type)
		if !ok {
			continue
		}

		fieldSpecs := provider.Flags(joinConfigPrefix(parentPrefix, fieldPrefix))
		if parentPrefix != "" {
			fieldSpecs = qualifyNestedFlagSpecs(fieldSpecs, parentPrefix)
		}
		specs = append(specs, fieldSpecs...)
	}
	return specs
}

// RegisterConfigFlags 将配置聚合对象声明的 FlagSpec 注册为 Cobra persistent flags。
// FlagSpec.Name 是规范参数名，Aliases 中的名称使用相同类型、默认值和帮助信息。
func RegisterConfigFlags(cmd *cobra.Command, specs []FlagSpec) error {
	for _, spec := range specs {
		if spec.Name == "" {
			continue
		}

		if err := definePersistentFlag(cmd, spec.Name, spec.Shorthand, spec.Default, spec.Usage); err != nil {
			return err
		}

		// alias 与规范参数使用相同类型和默认值；加载阶段仍以 spec.Name 作为 Viper 配置键。
		for _, alias := range spec.Aliases {
			if alias == "" {
				continue
			}
			if err := definePersistentFlag(cmd, alias, "", spec.Default, spec.Usage); err != nil {
				return err
			}
		}
	}

	return nil
}

// LoadViperConfig 根据已解析的 Cobra 命令构造配置加载选项，加载并校验最终的 T。
//
// 默认配置文件不存在时允许继续启动，以便依赖远程配置、环境变量或 CLI；如果用户显式传入
// --config，则文件不存在也视为输入错误。--remote 可与本地文件同时使用，远程配置优先级低于
// 本地文件；远程读取失败时只有已成功读取的本地文件可以作为兜底。
func LoadViperConfig[T any](cmd *cobra.Command, opts CommandOptions[T], specs []FlagSpec) (*T, error) {
	// cmd.Flags() 在 RunE 阶段已包含当前命令可见的 persistent/local flags 及其 Changed 状态。
	configFile, err := cmd.Flags().GetString("config")
	if err != nil {
		return nil, err
	}
	remoteConfig, err := cmd.Flags().GetString("remote")
	if err != nil {
		return nil, err
	}

	loaderOptions := make([]ConfigureLoaderOption, 0, 4)
	if configFile != "" {
		// Changed 用于区分“使用默认路径”和“用户明确指定路径”，两者的缺失容忍策略不同。
		configFlag := cmd.Flags().Lookup("config")
		configExplicit := configFlag != nil && configFlag.Changed
		ignoreNotFound := !configExplicit && configFile == opts.DefaultFile
		loaderOptions = append(loaderOptions, WithConfigFile(configFile, ignoreNotFound))
	}
	if remoteConfig != "" {
		loaderOptions = append(loaderOptions, WithRemoteURL(remoteConfig))
	}
	if opts.EnvPrefix != "" {
		loaderOptions = append(loaderOptions, WithEnvPrefix(opts.EnvPrefix))
	}
	if opts.StrictDecode {
		loaderOptions = append(loaderOptions, WithStrictDecode())
	}

	// Cobra 参数始终加入加载器；Viper 自身按 flag > env > local config > remote KV > default 处理优先级。
	loaderOptions = append(loaderOptions, WithCommand(cmd, specs))

	// 每次命令执行创建独立配置对象，不复用上一次执行产生的状态。
	cfg := new(T)
	if err := InitConfigure(cfg, loaderOptions...); err != nil {
		return nil, err
	}
	return cfg, nil
}

// definePersistentFlag 根据默认值的 Go 类型选择对应的 Cobra/pflag 注册函数。
//
// 同一个命令中参数名和 shorthand 必须唯一。当前支持基础数值类型、bool、string、
// time.Duration 和 []string；其它类型应先在配置项中转换为受支持的 CLI 表达。
func definePersistentFlag(cmd *cobra.Command, name, shorthand string, defaultValue any, usage string) error {
	flags := cmd.PersistentFlags()
	if flags.Lookup(name) != nil {
		return fmt.Errorf("flag %q already defined", name)
	}
	if shorthand != "" && flags.ShorthandLookup(shorthand) != nil {
		return fmt.Errorf("short flag %q already defined", shorthand)
	}

	// 使用类型开关保留 Cobra 对参数格式和范围的原生校验能力。
	switch value := defaultValue.(type) {
	case string:
		flags.StringP(name, shorthand, value, usage)
	case bool:
		flags.BoolP(name, shorthand, value, usage)
	case int:
		flags.IntP(name, shorthand, value, usage)
	case int8:
		flags.Int8P(name, shorthand, value, usage)
	case int16:
		flags.Int16P(name, shorthand, value, usage)
	case int32:
		flags.Int32P(name, shorthand, value, usage)
	case int64:
		flags.Int64P(name, shorthand, value, usage)
	case uint:
		flags.UintP(name, shorthand, value, usage)
	case uint8:
		flags.Uint8P(name, shorthand, value, usage)
	case uint16:
		flags.Uint16P(name, shorthand, value, usage)
	case uint32:
		flags.Uint32P(name, shorthand, value, usage)
	case uint64:
		flags.Uint64P(name, shorthand, value, usage)
	case float32:
		flags.Float32P(name, shorthand, value, usage)
	case float64:
		flags.Float64P(name, shorthand, value, usage)
	case time.Duration:
		flags.DurationP(name, shorthand, value, usage)
	case []string:
		flags.StringSliceP(name, shorthand, value, usage)
	case nil:
		flags.StringP(name, shorthand, "", usage)
	default:
		return fmt.Errorf("unsupported flag %q default type %T", name, defaultValue)
	}
	return nil
}

// configPrefix 返回聚合字段对应的 Viper 配置前缀。
// mapstructure tag 优先；tag 中逗号后的选项会被剔除；未声明 tag 时回退到字段名。
func configPrefix(field reflect.StructField) string {
	if name := field.Tag.Get("mapstructure"); name != "" {
		return strings.Split(name, ",")[0]
	}
	return field.Name
}

func joinConfigPrefix(parent, child string) string {
	parent = strings.Trim(parent, ".")
	child = strings.Trim(child, ".")
	switch {
	case parent == "":
		return child
	case child == "":
		return parent
	default:
		return parent + "." + child
	}
}

func qualifyNestedFlagSpecs(specs []FlagSpec, parentPrefix string) []FlagSpec {
	qualified := make([]FlagSpec, len(specs))
	for i, spec := range specs {
		qualified[i] = spec
		qualified[i].Shorthand = ""
		if len(spec.Aliases) == 0 {
			continue
		}
		qualified[i].Aliases = make([]string, 0, len(spec.Aliases))
		for _, alias := range spec.Aliases {
			qualified[i].Aliases = append(qualified[i].Aliases, joinConfigPrefix(parentPrefix, alias))
		}
	}
	return qualified
}

// flagProvider 为配置字段类型创建一个零值实例，并判断它是否实现 FlagProvider。
//
// 先剥离字段上的指针层级，再分别检查 *T 和 T，因而 Flags 可以使用指针接收者
// 或值接收者实现，同时不会要求业务聚合对象提前初始化嵌套配置指针。
func flagProvider(typ reflect.Type) (FlagProvider, bool) {
	for typ.Kind() == reflect.Ptr {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct {
		return nil, false
	}

	ptr := reflect.New(typ)
	if provider, ok := ptr.Interface().(FlagProvider); ok {
		return provider, true
	}
	if ptr.Elem().CanInterface() {
		if provider, ok := ptr.Elem().Interface().(FlagProvider); ok {
			return provider, true
		}
	}
	return nil, false
}

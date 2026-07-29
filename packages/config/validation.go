package config

import (
	"errors"
	"reflect"
	"sort"
	"strings"

	validator "github.com/go-playground/validator/v10"

	"github.com/wplbyx/modular/packages/config/configitem"
)

// Violation 描述一个不包含配置实际值的校验失败。
type Violation struct {
	Path  string
	Rule  string
	Param string
}

// ValidationError 汇总配置对象的全部字段和结构级校验失败。
type ValidationError struct {
	Violations []Violation
}

func (e *ValidationError) Error() string {
	if e == nil || len(e.Violations) == 0 {
		return "configuration validation failed"
	}

	var builder strings.Builder
	builder.WriteString("configuration validation failed")
	for _, violation := range e.Violations {
		builder.WriteString(": ")
		builder.WriteString(violation.Path)
		builder.WriteString(" failed ")
		builder.WriteString(violation.Rule)
		if violation.Param != "" {
			builder.WriteString(" (")
			builder.WriteString(violation.Param)
			builder.WriteByte(')')
		}
	}
	return builder.String()
}

// Validate 执行 validate tag 规则和内置配置项的跨字段规则。
func Validate(object interface{}) error {
	validate := validator.New()
	registerStructValidations(validate)

	if err := validate.Struct(object); err != nil {
		var fieldErrors validator.ValidationErrors
		if !errors.As(err, &fieldErrors) {
			return err
		}

		rootType := reflect.TypeOf(object)
		violations := make([]Violation, 0, len(fieldErrors))
		for _, fieldError := range fieldErrors {
			violations = append(violations, Violation{
				Path:  canonicalValidationPath(rootType, fieldError.StructNamespace()),
				Rule:  fieldError.Tag(),
				Param: fieldError.Param(),
			})
		}
		sort.SliceStable(violations, func(i, j int) bool {
			if violations[i].Path != violations[j].Path {
				return violations[i].Path < violations[j].Path
			}
			return violations[i].Rule < violations[j].Rule
		})
		return &ValidationError{Violations: violations}
	}
	return nil
}

func registerStructValidations(validate *validator.Validate) {
	validate.RegisterStructValidation(validateStorage, configitem.Storage{})
	validate.RegisterStructValidation(validateMongo, configitem.Mongo{})
	validate.RegisterStructValidation(validateRedis, configitem.Redis{})
	validate.RegisterStructValidation(validateHTTP, configitem.HTTP{})
}

func validateStorage(level validator.StructLevel) {
	cfg := level.Current().Interface().(configitem.Storage)
	switch cfg.Type {
	case "disk":
		if cfg.Disk == nil {
			level.ReportError(cfg.Disk, "Disk", "Disk", "required", "")
			return
		}
		if strings.TrimSpace(cfg.Disk.RootDir) == "" {
			level.ReportError(cfg.Disk.RootDir, "Disk.RootDir", "Disk.RootDir", "required", "")
		}
	case "oss":
		if cfg.OSS == nil {
			level.ReportError(cfg.OSS, "OSS", "OSS", "required", "")
			return
		}
		reportRequired(level, "OSS.AccessKeyID", cfg.OSS.AccessKeyID)
		reportRequired(level, "OSS.AccessKeySecret", cfg.OSS.AccessKeySecret)
		reportRequired(level, "OSS.Region", cfg.OSS.Region)
		reportRequired(level, "OSS.Bucket", cfg.OSS.Bucket)
	}
}

func validateMongo(level validator.StructLevel) {
	cfg := level.Current().Interface().(configitem.Mongo)
	hasURI := strings.TrimSpace(cfg.URI) != ""
	hasHosts := len(cfg.Hosts) > 0

	if !hasURI && !hasHosts {
		level.ReportError(cfg.URI, "URI", "URI", "required_without", "Hosts")
	}
	if hasURI && hasHosts {
		level.ReportError(cfg.Hosts, "Hosts", "Hosts", "excluded_with", "URI")
	}
	if hasURI && !strings.HasPrefix(cfg.URI, "mongodb://") && !strings.HasPrefix(cfg.URI, "mongodb+srv://") {
		level.ReportError(cfg.URI, "URI", "URI", "mongodb_uri", "")
	}
}

func validateRedis(level validator.StructLevel) {
	cfg := level.Current().Interface().(configitem.Redis)
	if len(cfg.Urls) > 0 {
		return
	}
	if strings.TrimSpace(cfg.Host) == "" {
		level.ReportError(cfg.Host, "Host", "Host", "required_without", "Urls")
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		level.ReportError(cfg.Port, "Port", "Port", "tcp_port", "")
	}
}

func validateHTTP(level validator.StructLevel) {
	cfg := level.Current().Interface().(configitem.HTTP)
	if !cfg.EnableTLS {
		return
	}
	reportRequired(level, "TLSCertFile", cfg.TLSCertFile)
	reportRequired(level, "TLSKeyFile", cfg.TLSKeyFile)
}

func reportRequired(level validator.StructLevel, path, value string) {
	if strings.TrimSpace(value) == "" {
		level.ReportError(value, path, path, "required", "")
	}
}

func canonicalValidationPath(rootType reflect.Type, namespace string) string {
	for rootType != nil && rootType.Kind() == reflect.Ptr {
		rootType = rootType.Elem()
	}
	if rootType == nil {
		return namespace
	}

	segments := strings.Split(namespace, ".")
	if len(segments) > 0 && segments[0] == rootType.Name() {
		segments = segments[1:]
	}

	current := rootType
	path := make([]string, 0, len(segments))
	for _, segment := range segments {
		fieldName, suffix := splitValidationSegment(segment)
		for current.Kind() == reflect.Ptr {
			current = current.Elem()
		}
		if current.Kind() != reflect.Struct {
			path = append(path, segment)
			continue
		}

		field, ok := current.FieldByName(fieldName)
		if !ok {
			path = append(path, segment)
			continue
		}

		name, squash := mapstructureFieldName(field)
		if !squash {
			path = append(path, name+suffix)
		}
		current = field.Type
	}
	return strings.Join(path, ".")
}

func splitValidationSegment(segment string) (string, string) {
	if index := strings.IndexByte(segment, '['); index >= 0 {
		return segment[:index], segment[index:]
	}
	return segment, ""
}

func mapstructureFieldName(field reflect.StructField) (string, bool) {
	tag, ok := field.Tag.Lookup("mapstructure")
	if !ok {
		return field.Name, false
	}
	parts := strings.Split(tag, ",")
	name := parts[0]
	if name == "" {
		name = field.Name
	}
	for _, option := range parts[1:] {
		if option == "squash" {
			return name, true
		}
	}
	if name == "-" {
		return field.Name, false
	}
	return name, false
}

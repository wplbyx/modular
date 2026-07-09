package command

import (
	"fmt"
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/pflag"
)

// NewParseCommands parses command-line flags into a struct pointer.
func NewParseCommands(object interface{}) error {
	return ParseCommands(os.Args[1:], object)
}

func ParseCommands(args []string, object interface{}) error {
	if object == nil {
		return fmt.Errorf("command target is nil")
	}

	value := reflect.ValueOf(object)
	if value.Kind() != reflect.Ptr || value.IsNil() {
		return fmt.Errorf("command target must be a non-nil struct pointer")
	}
	value = value.Elem()
	if value.Kind() != reflect.Struct {
		return fmt.Errorf("command target must point to a struct")
	}

	flags := pflag.NewFlagSet(os.Args[0], pflag.ContinueOnError)
	flags.SetOutput(io.Discard)
	bindings, err := bindStructFlags(flags, value, "")
	if err != nil {
		return err
	}
	if err := flags.Parse(args); err != nil {
		return err
	}
	return validateRequiredFlags(flags, bindings)
}

type flagBinding struct {
	name     string
	field    reflect.Value
	required bool
}

func bindStructFlags(flags *pflag.FlagSet, value reflect.Value, prefix string) ([]flagBinding, error) {
	typ := value.Type()
	bindings := make([]flagBinding, 0, typ.NumField())

	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		fieldValue := value.Field(i)
		if field.PkgPath != "" || !fieldValue.CanSet() {
			continue
		}
		if field.Anonymous && fieldValue.Kind() == reflect.Struct {
			nested, err := bindStructFlags(flags, fieldValue, prefix)
			if err != nil {
				return nil, err
			}
			bindings = append(bindings, nested...)
			continue
		}

		name := flagName(field)
		if name == "-" {
			continue
		}
		if prefix != "" {
			name = prefix + "." + name
		}
		if isNestedStruct(fieldValue) {
			nested, err := bindStructFlags(flags, fieldValue, name)
			if err != nil {
				return nil, err
			}
			bindings = append(bindings, nested...)
			continue
		}

		usage := field.Tag.Get("usage")
		short := field.Tag.Get("short")
		defaultValue := field.Tag.Get("default")
		if defaultValue == "" {
			defaultValue = valueString(fieldValue)
		}

		if err := bindFlag(flags, name, short, usage, defaultValue, fieldValue); err != nil {
			return nil, fmt.Errorf("bind flag %s: %w", name, err)
		}
		bindings = append(bindings, flagBinding{
			name:     name,
			field:    fieldValue,
			required: field.Tag.Get("required") == "true",
		})
	}

	return bindings, nil
}

func flagName(field reflect.StructField) string {
	if name := field.Tag.Get("flag"); name != "" {
		return name
	}
	if name := field.Tag.Get("mapstructure"); name != "" {
		return strings.ToLower(strings.Split(name, ",")[0])
	}
	return strings.ToLower(field.Name)
}

func bindFlag(flags *pflag.FlagSet, name, short, usage, defaultValue string, field reflect.Value) error {
	if flags.Lookup(name) != nil {
		return fmt.Errorf("flag %q already defined", name)
	}
	if short != "" && flags.ShorthandLookup(short) != nil {
		return fmt.Errorf("short flag %q already defined", short)
	}

	switch field.Kind() {
	case reflect.String:
		flags.StringVarP(field.Addr().Interface().(*string), name, short, defaultValue, usage)
	case reflect.Bool:
		val, err := strconv.ParseBool(defaultIfEmpty(defaultValue, "false"))
		if err != nil {
			return err
		}
		flags.BoolVarP(field.Addr().Interface().(*bool), name, short, val, usage)
	case reflect.Int:
		if field.Type() == reflect.TypeOf(time.Duration(0)) {
			val, err := time.ParseDuration(defaultIfEmpty(defaultValue, "0s"))
			if err != nil {
				return err
			}
			flags.DurationVarP(field.Addr().Interface().(*time.Duration), name, short, val, usage)
			return nil
		}
		val, err := strconv.Atoi(defaultIfEmpty(defaultValue, "0"))
		if err != nil {
			return err
		}
		flags.IntVarP(field.Addr().Interface().(*int), name, short, val, usage)
	case reflect.Int8:
		val, err := strconv.ParseInt(defaultIfEmpty(defaultValue, "0"), 10, 8)
		if err != nil {
			return err
		}
		flags.Int8VarP(field.Addr().Interface().(*int8), name, short, int8(val), usage)
	case reflect.Int16:
		val, err := strconv.ParseInt(defaultIfEmpty(defaultValue, "0"), 10, 16)
		if err != nil {
			return err
		}
		flags.Int16VarP(field.Addr().Interface().(*int16), name, short, int16(val), usage)
	case reflect.Int32:
		val, err := strconv.ParseInt(defaultIfEmpty(defaultValue, "0"), 10, 32)
		if err != nil {
			return err
		}
		flags.Int32VarP(field.Addr().Interface().(*int32), name, short, int32(val), usage)
	case reflect.Int64:
		if field.Type() == reflect.TypeOf(time.Duration(0)) {
			val, err := time.ParseDuration(defaultIfEmpty(defaultValue, "0s"))
			if err != nil {
				return err
			}
			flags.DurationVarP(field.Addr().Interface().(*time.Duration), name, short, val, usage)
			return nil
		}
		val, err := strconv.ParseInt(defaultIfEmpty(defaultValue, "0"), 10, 64)
		if err != nil {
			return err
		}
		flags.Int64VarP(field.Addr().Interface().(*int64), name, short, val, usage)
	case reflect.Uint:
		val, err := strconv.ParseUint(defaultIfEmpty(defaultValue, "0"), 10, 0)
		if err != nil {
			return err
		}
		flags.UintVarP(field.Addr().Interface().(*uint), name, short, uint(val), usage)
	case reflect.Uint8:
		val, err := strconv.ParseUint(defaultIfEmpty(defaultValue, "0"), 10, 8)
		if err != nil {
			return err
		}
		flags.Uint8VarP(field.Addr().Interface().(*uint8), name, short, uint8(val), usage)
	case reflect.Uint16:
		val, err := strconv.ParseUint(defaultIfEmpty(defaultValue, "0"), 10, 16)
		if err != nil {
			return err
		}
		flags.Uint16VarP(field.Addr().Interface().(*uint16), name, short, uint16(val), usage)
	case reflect.Uint32:
		val, err := strconv.ParseUint(defaultIfEmpty(defaultValue, "0"), 10, 32)
		if err != nil {
			return err
		}
		flags.Uint32VarP(field.Addr().Interface().(*uint32), name, short, uint32(val), usage)
	case reflect.Uint64:
		val, err := strconv.ParseUint(defaultIfEmpty(defaultValue, "0"), 10, 64)
		if err != nil {
			return err
		}
		flags.Uint64VarP(field.Addr().Interface().(*uint64), name, short, val, usage)
	case reflect.Float32:
		val, err := strconv.ParseFloat(defaultIfEmpty(defaultValue, "0"), 32)
		if err != nil {
			return err
		}
		flags.Float32VarP(field.Addr().Interface().(*float32), name, short, float32(val), usage)
	case reflect.Float64:
		val, err := strconv.ParseFloat(defaultIfEmpty(defaultValue, "0"), 64)
		if err != nil {
			return err
		}
		flags.Float64VarP(field.Addr().Interface().(*float64), name, short, val, usage)
	case reflect.Slice:
		if field.Type().Elem().Kind() != reflect.String {
			return fmt.Errorf("unsupported slice type %s", field.Type())
		}
		defaults := splitDefaultList(defaultValue)
		flags.StringSliceVarP(field.Addr().Interface().(*[]string), name, short, defaults, usage)
	default:
		return fmt.Errorf("unsupported field type %s", field.Type())
	}
	return nil
}

func isNestedStruct(value reflect.Value) bool {
	return value.Kind() == reflect.Struct && value.Type() != reflect.TypeOf(time.Duration(0))
}

func validateRequiredFlags(flags *pflag.FlagSet, bindings []flagBinding) error {
	for _, binding := range bindings {
		if !binding.required {
			continue
		}
		flag := flags.Lookup(binding.name)
		if flag == nil || !flag.Changed {
			return fmt.Errorf("required flag %q is missing", binding.name)
		}
	}
	return nil
}

func valueString(value reflect.Value) string {
	if value.Type() == reflect.TypeOf(time.Duration(0)) {
		return value.Interface().(time.Duration).String()
	}
	switch value.Kind() {
	case reflect.String:
		return value.String()
	case reflect.Bool:
		return strconv.FormatBool(value.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(value.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(value.Uint(), 10)
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(value.Float(), 'f', -1, value.Type().Bits())
	case reflect.Slice:
		if value.Type().Elem().Kind() == reflect.String {
			parts := make([]string, value.Len())
			for i := 0; i < value.Len(); i++ {
				parts[i] = value.Index(i).String()
			}
			return strings.Join(parts, ",")
		}
	}
	return ""
}

func defaultIfEmpty(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func splitDefaultList(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := parts[:0]
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func isZeroValue(value reflect.Value) bool {
	return value.IsZero()
}

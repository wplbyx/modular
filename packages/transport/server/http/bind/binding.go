package bind

import (
	"encoding"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
)

// RegisterCustomValidator registers a custom validator
func RegisterCustomValidator(v *validator.Validate) {
	if v, ok := binding.Validator.Engine().(*validator.Validate); ok {
		// Register custom validations here
		_ = v
	}
}

// BindJSON binds JSON body to struct with validation
func BindJSON(c *gin.Context, obj interface{}) error {
	if err := c.ShouldBindJSON(obj); err != nil {
		return err
	}
	return nil
}

// QueryBinding binds query parameters by json tags. Gin's default query binder
// uses form tags, but most request DTOs in this project already use json tags.
type QueryBinding struct{}

func (QueryBinding) Name() string {
	return "json-query"
}

func (QueryBinding) Bind(req *http.Request, obj interface{}) error {
	if err := bindJSONTagQuery(req.URL.Query(), obj); err != nil {
		return err
	}
	if binding.Validator != nil {
		return binding.Validator.ValidateStruct(obj)
	}
	return nil
}

// BindQuery binds query parameters to struct with validation
func BindQuery(c *gin.Context, obj interface{}) error {
	if err := (QueryBinding{}).Bind(c.Request, obj); err != nil {
		return err
	}
	return nil
}

// BindURI binds URI parameters to struct with validation
func BindURI(c *gin.Context, obj interface{}) error {
	if err := c.ShouldBindUri(obj); err != nil {
		return err
	}
	return nil
}

func bindJSONTagQuery(values url.Values, obj interface{}) error {
	if obj == nil {
		return nil
	}

	value := reflect.ValueOf(obj)
	if value.Kind() != reflect.Ptr || value.IsNil() {
		return errors.New("obj must be a non-nil pointer")
	}

	value = value.Elem()
	if value.Kind() != reflect.Struct {
		return errors.New("obj must point to a struct")
	}

	return bindStruct(values, value)
}

func bindStruct(values url.Values, value reflect.Value) error {
	typ := value.Type()
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		fieldValue := value.Field(i)
		if !fieldValue.CanSet() {
			continue
		}

		if field.Anonymous && fieldValue.Kind() == reflect.Struct {
			if err := bindStruct(values, fieldValue); err != nil {
				return err
			}
			continue
		}

		paramName := queryName(field)
		if paramName == "" {
			continue
		}

		paramValues, ok := values[paramName]
		if !ok || len(paramValues) == 0 {
			continue
		}

		if err := setField(fieldValue, paramValues); err != nil {
			return fmt.Errorf("set query field %s: %w", field.Name, err)
		}
	}
	return nil
}

func queryName(field reflect.StructField) string {
	jsonTag := field.Tag.Get("json")
	if jsonTag == "-" {
		return ""
	}
	if jsonTag != "" {
		name := strings.Split(jsonTag, ",")[0]
		if name != "" {
			return name
		}
	}
	formTag := field.Tag.Get("form")
	if formTag == "-" {
		return ""
	}
	if formTag != "" {
		return strings.Split(formTag, ",")[0]
	}
	return ""
}

func setField(field reflect.Value, values []string) error {
	if len(values) == 0 {
		return nil
	}

	fieldType := field.Type()
	if fieldType.Kind() == reflect.Slice {
		slice := reflect.MakeSlice(fieldType, len(values), len(values))
		for i, value := range values {
			if err := setValue(slice.Index(i), fieldType.Elem(), value); err != nil {
				return err
			}
		}
		field.Set(slice)
		return nil
	}

	return setValue(field, fieldType, values[0])
}

func setValue(field reflect.Value, fieldType reflect.Type, value string) error {
	if value == "" {
		return nil
	}

	if fieldType == reflect.TypeOf(time.Duration(0)) {
		duration, err := time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("invalid duration value %q", value)
		}
		field.SetInt(int64(duration))
		return nil
	}

	textUnmarshalerType := reflect.TypeOf((*encoding.TextUnmarshaler)(nil)).Elem()
	if field.CanAddr() && reflect.PointerTo(fieldType).Implements(textUnmarshalerType) {
		unmarshaler := field.Addr().Interface().(encoding.TextUnmarshaler)
		if err := unmarshaler.UnmarshalText([]byte(value)); err != nil {
			return fmt.Errorf("invalid text value %q: %w", value, err)
		}
		return nil
	}

	switch fieldType.Kind() {
	case reflect.String:
		field.SetString(value)
	case reflect.Bool:
		b, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid boolean value %q", value)
		}
		field.SetBool(b)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(value, 10, fieldType.Bits())
		if err != nil {
			return fmt.Errorf("invalid integer value %q", value)
		}
		field.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(value, 10, fieldType.Bits())
		if err != nil {
			return fmt.Errorf("invalid unsigned integer value %q", value)
		}
		field.SetUint(n)
	case reflect.Float32, reflect.Float64:
		n, err := strconv.ParseFloat(value, fieldType.Bits())
		if err != nil {
			return fmt.Errorf("invalid float value %q", value)
		}
		field.SetFloat(n)
	case reflect.Ptr:
		if field.IsNil() {
			field.Set(reflect.New(fieldType.Elem()))
		}
		return setValue(field.Elem(), fieldType.Elem(), value)
	default:
		return fmt.Errorf("unsupported field type %s", fieldType.Kind())
	}
	return nil
}

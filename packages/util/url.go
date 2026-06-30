package util

import (
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// ToURLParams 将对象转换为 URL 查询参数字符串
// 支持 proto.Message 和普通 struct
// 例如: {Name: "test", Age: 18} => "name=test&age=18"
// 只支持一层结构，不支持嵌套
func ToURLParams(obj interface{}) (string, error) {
	if obj == nil {
		return "", nil
	}

	// 优先处理 proto.Message
	if msg, ok := obj.(proto.Message); ok {
		return toURLParamsFromProto(msg)
	}

	// 处理普通 struct
	return toURLParamsFromStruct(obj)
}

// toURLParamsFromProto 从 proto 消息转换 URL 参数
func toURLParamsFromProto(msg proto.Message) (string, error) {
	jsonBytes, err := protojson.Marshal(msg)
	if err != nil {
		return "", fmt.Errorf("protojson marshal failed: %w", err)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(jsonBytes, &data); err != nil {
		return "", fmt.Errorf("json unmarshal failed: %w", err)
	}

	values := url.Values{}
	for key, value := range data {
		if isZeroValue(value) {
			continue
		}
		values.Set(key, fmt.Sprintf("%v", value))
	}

	return values.Encode(), nil
}

// toURLParamsFromStruct 从普通 struct 转换 URL 参数
func toURLParamsFromStruct(obj interface{}) (string, error) {
	v := reflect.ValueOf(obj)
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return "", nil
		}
		v = v.Elem()
	}

	if v.Kind() != reflect.Struct {
		return "", fmt.Errorf("input must be a struct or pointer to struct or proto.Message, got %T", obj)
	}

	values := url.Values{}
	t := v.Type()

	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		fieldType := t.Field(i)

		// 跳过未导出字段 (首字母小写)
		if fieldType.PkgPath != "" {
			continue
		}

		// 获取参数名：优先使用 json tag，否则使用字段名
		paramName := getParamName(fieldType)
		if paramName == "" || paramName == "-" {
			continue
		}

		// 获取字段值
		fieldValue := getFieldValue(field)
		if fieldValue == "" {
			continue
		}

		values.Set(paramName, fieldValue)
	}

	return values.Encode(), nil
}

// getParamName 获取字段对应的参数名
func getParamName(fieldType reflect.StructField) string {
	// 优先使用 json tag
	jsonTag := fieldType.Tag.Get("json")
	if jsonTag != "" {
		return parseJSONTagName(jsonTag)
	}

	// 其次使用 form tag
	formTag := fieldType.Tag.Get("form")
	if formTag != "" {
		return parseJSONTagName(formTag)
	}

	// 最后使用字段名（转为小写）
	return toSnakeCase(fieldType.Name)
}

// parseJSONTagName 解析 tag 中的字段名
func parseJSONTagName(tag string) string {
	for i := 0; i < len(tag); i++ {
		if tag[i] == ',' || tag[i] == ' ' {
			return tag[:i]
		}
	}
	return tag
}

// toSnakeCase 将驼峰命名转为下划线命名
func toSnakeCase(s string) string {
	var result []rune
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			result = append(result, '_')
		}
		result = append(result, r)
	}
	return string(result)
}

// getFieldValue 获取字段的字符串值
func getFieldValue(field reflect.Value) string {
	switch field.Kind() {
	case reflect.String:
		return field.String()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return fmt.Sprintf("%d", field.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return fmt.Sprintf("%d", field.Uint())
	case reflect.Float32, reflect.Float64:
		return fmt.Sprintf("%f", field.Float())
	case reflect.Bool:
		return fmt.Sprintf("%t", field.Bool())
	case reflect.Ptr, reflect.Interface:
		if field.IsNil() {
			return ""
		}
		return getFieldValue(field.Elem())
	default:
		return ""
	}
}

// isZeroValue 判断值是否为零值
func isZeroValue(v interface{}) bool {
	if v == nil {
		return true
	}

	switch val := v.(type) {
	case string:
		return val == ""
	case bool:
		return !val
	case int, int8, int16, int32, int64:
		return v == 0
	case uint, uint8, uint16, uint32, uint64:
		return v == 0
	case float32, float64:
		return v == 0.0
	case []interface{}:
		return len(val) == 0
	case map[string]interface{}:
		return len(val) == 0
	default:
		return false
	}
}

// BuildURL 构建 URL，将对象参数附加到基础 URL 后
// 例如: BuildURL("http://example.com/api", obj) => "http://example.com/api?name=test&age=18"
func BuildURL(baseURL string, obj interface{}) (string, error) {
	params, err := ToURLParams(obj)
	if err != nil {
		return "", err
	}
	if params == "" {
		return baseURL, nil
	}
	return baseURL + "?" + params, nil
}

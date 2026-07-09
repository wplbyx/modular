package errs

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
)

// ErrorType 错误类型（可选，用于快速分类，实际可以通过 code 范围判断）
type ErrorType int

const (
	BusinessError ErrorType = 1 // 业务错误
	SystemError   ErrorType = 2 // 系统错误
)

// CustomErrorOption 定义错误选项函数类型
type CustomErrorOption func(*CustomError)

// CustomError 自定义错误结构
type CustomError struct {
	cause  error                  // 底层原始错误
	code   int32                  // 错误码 (直接使用 Proto Enum 的值)
	msg    string                 // 错误提示信息
	fields map[string]interface{} // 附加的上下文信息
	stack  string                 // 堆栈信息 (在创建时自动捕获)
}

// 确保 CustomError 实现了 error 接口
var _ error = (*CustomError)(nil)

// 确保 CustomError 实现了 Unwrap 接口 (支持 errors.Is/As)
var _ interface{ Unwrap() error } = (*CustomError)(nil)

// ==================== Option 构造函数 ====================

// WithCode 设置错误码 (通常传入 Proto Enum 生成的常量)
func WithCode(code int32) CustomErrorOption {
	return func(e *CustomError) {
		e.code = code
	}
}

// WithMsgf 设置错误信息 (支持格式化)
func WithMsgf(format string, args ...interface{}) CustomErrorOption {
	return func(e *CustomError) {
		e.msg = fmt.Sprintf(format, args...)
	}
}

// WithCause 包装底层错误
func WithCause(err error) CustomErrorOption {
	return func(e *CustomError) {
		e.cause = err
	}
}

// WithField 设置单个上下文字段
func WithField(key string, value interface{}) CustomErrorOption {
	return func(e *CustomError) {
		if e.fields == nil {
			e.fields = make(map[string]interface{})
		}
		e.fields[key] = value
	}
}

// ==================== 核心构造函数 ====================

// New 创建一个新的业务错误
// 默认会自动捕获当前调用堆栈
func New(opts ...CustomErrorOption) *CustomError {
	e := &CustomError{
		stack: captureStack(2), // 跳过 captureStack 和 New
	}

	for _, opt := range opts {
		opt(e)
	}

	return e.check()
}

// Wrap 包装一个已存在的 error
// 如果 err 是 nil，返回 nil
// 如果 err 已经是 CustomError，会保留其原始信息并允许 Option 覆盖
func Wrap(err error, opts ...CustomErrorOption) *CustomError {
	if err == nil {
		return nil
	}

	e := &CustomError{
		stack: captureStack(2), // 跳过 captureStack 和 Wrap
	}

	// 如果被包装的是 CustomError，继承其属性
	var origin *CustomError
	if errors.As(err, &origin) {
		e.cause = err // 保留完整的中间错误链
		e.code = origin.code
		e.msg = origin.msg
		// 合并 fields
		if len(origin.fields) > 0 {
			e.fields = make(map[string]interface{})
			for k, v := range origin.fields {
				e.fields[k] = v
			}
		}
	} else {
		// 如果是普通 error，作为 cause
		e.cause = err
		// 如果没有指定 msg，暂时使用 err.Error()，check() 中会再次处理
		if e.msg == "" {
			e.msg = err.Error()
		}
	}

	// 应用新选项，新选项会覆盖继承的属性
	for _, opt := range opts {
		opt(e)
	}

	return e.check()
}

// ==================== 内部辅助方法 ====================

// check 检查并完善错误对象
func (e *CustomError) check() *CustomError {
	// 防御性编程：如果既没有 Code 也没有 Msg，说明是个无效的错误构造
	// 这时候最好返回一个默认的错误，而不是 nil，以免掩盖错误
	if e.code == 0 && e.msg == "" && e.cause == nil {
		return &CustomError{
			code:  1000, // 默认未知错误
			msg:   "unknown error",
			stack: captureStack(1),
		}
	}

	// 只有 msg 为空时，尝试从 cause 或者 code 获取
	if e.msg == "" {
		if e.cause != nil {
			e.msg = e.cause.Error()
		} else {
			e.msg = "error occurred"
		}
	}

	return e
}

// captureStack 捕获调用堆栈
// skip: 跳过的栈帧数
func captureStack(skip int) string {
	const maxDepth = 32
	pcs := make([]uintptr, maxDepth)
	// skip + 2 是为了跳过 runtime.Callers 和 captureStack 本身
	n := runtime.Callers(skip+2, pcs)
	if n == 0 {
		return ""
	}

	frames := runtime.CallersFrames(pcs[:n])
	var sb strings.Builder
	for {
		frame, more := frames.Next()
		// 简化文件名，只保留最后部分
		file := frame.File
		if idx := strings.LastIndex(file, "/"); idx != -1 {
			file = file[idx+1:]
		}
		sb.WriteString(fmt.Sprintf("%s:%d %s\n", file, frame.Line, frame.Function))
		if !more {
			break
		}
	}
	return sb.String()
}

// ==================== 接口实现 ====================

// Error 实现 error 接口
func (e *CustomError) Error() string {
	if e.msg == "" {
		return "error"
	}
	// 如果有 Code，优先展示 Code
	if e.code != 0 {
		return fmt.Sprintf("[Code:%d] %s", e.code, e.msg)
	}
	return e.msg
}

// Unwrap 实现 errors.Unwrap，支持错误链解包
func (e *CustomError) Unwrap() error {
	return e.cause
}

// ==================== 扩展方法 ====================

// Code 获取错误码
func (e *CustomError) Code() int32 {
	return e.code
}

// Fields 获取附加字段
func (e *CustomError) Fields() map[string]interface{} {
	if len(e.fields) == 0 {
		return nil
	}
	fields := make(map[string]interface{}, len(e.fields))
	for k, v := range e.fields {
		fields[k] = v
	}
	return fields
}

// FullErrStack 生成完整的错误报告，包含堆栈和上下文
// 适合用于日志输出
func (e *CustomError) FullErrStack() string {
	if e == nil {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("========== Error Stack ==========\n")

	// 1. 错误链信息
	sb.WriteString("Error Chain:\n")
	currentErr := error(e)
	depth := 0
	for currentErr != nil {
		depth++
		sb.WriteString(fmt.Sprintf("  [%d] %v\n", depth, currentErr.Error()))
		if ce, ok := currentErr.(*CustomError); ok {
			// 如果是 CustomError，打印 Code 和 Fields
			sb.WriteString(fmt.Sprintf("      -> Code: %d\n", ce.code))
			if len(ce.fields) > 0 {
				sb.WriteString(fmt.Sprintf("      -> Context: %+v\n", ce.fields))
			}
			currentErr = ce.Unwrap()
		} else {
			// 标准错误，停止解包
			break
		}
	}

	// 2. 堆栈信息 (通常取最外层或最内层的堆栈，这里取最外层的创建堆栈)
	if e.stack != "" {
		sb.WriteString("\nCreation Stack:\n")
		sb.WriteString(e.stack)
	}

	sb.WriteString("==================================\n")
	return sb.String()
}

// Is 判断错误是否匹配目标错误码或类型
// 示例: errors.Is(err, errs.CodeSystemTimeout) // 假设 CodeSystemTimeout 是 *CustomError
// 注意：这里通常配合 errors.As 使用，或者简单地比对 Code
func (e *CustomError) Is(target error) bool {
	if t, ok := target.(*CustomError); ok {
		if e.code == 0 || t.code == 0 {
			return false
		}
		return e.code == t.code
	}
	return false
}

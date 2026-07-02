package log

import (
	"errors"
	"fmt"
	"io"
	stdlog "log"
	"os"
	"path/filepath"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"modular/packages/config"
)

// 全局日志管理器
var lm *LoggerManager

type LoggerManagerOption func(*LoggerManager)

// LoggerManager 日志管理器
type LoggerManager struct {
	config  *config.Logging
	level   zapcore.Level
	logger  *zap.Logger
	cores   []zapcore.Core
	closers []io.Closer // 用于存储需要关闭的资源 (如 DailyRotate)
}

// GetLogger 获取 zap logger 实例；未初始化时返回 NopLogger
func GetLogger() *zap.Logger {
	if lm != nil && lm.logger != nil {
		return lm.logger
	}
	return zap.NewNop()
}

// NewLoggerManager 创建日志管理器
func NewLoggerManager(cfg *config.Logging, options ...LoggerManagerOption) (*LoggerManager, error) {
	if cfg == nil {
		return nil, errors.New("logger config is nil")
	}

	// 创建日志目录
	if err := ensureLogDir(cfg.File.Filename); err != nil {
		return nil, fmt.Errorf("failed to create log directory: %w", err)
	}

	lm = &LoggerManager{
		config: cfg,
		level:  parseLevel(cfg.Level),
	}

	for _, option := range options {
		option(lm)
	}

	if len(lm.cores) == 0 {
		return nil, errors.New("logger config output is empty")
	}

	// 合并所有 Core
	core := zapcore.NewTee(lm.cores...)
	lm.logger = zap.New(core, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel))

	return lm, nil
}

// Close 清理资源
func (lm *LoggerManager) Close() {
	if lm.logger != nil {
		_ = lm.logger.Sync()
	}
	for _, closer := range lm.closers {
		_ = closer.Close()
	}
}

// ensureLogDir 确保日志目录存在
func ensureLogDir(filename string) error {
	dir := filepath.Dir(filename)
	if dir == "" {
		return nil
	}
	return os.MkdirAll(dir, 0755)
}

// parseLevel 将字符串解析为 zapcore.Level，解析失败时回退到 InfoLevel
func parseLevel(level string) zapcore.Level {
	var l zapcore.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		return zapcore.InfoLevel
	}
	return l
}

// ===== 全局日志函数 =====

// Debug 记录调试日志
func Debug(args ...interface{}) {
	if lm == nil {
		return
	}
	lm.logger.Sugar().Debug(args...)
}

// Info 记录信息日志
func Info(args ...interface{}) {
	if lm == nil {
		return
	}
	lm.logger.Sugar().Info(args...)
}

// Warn 记录警告日志
func Warn(args ...interface{}) {
	if lm == nil {
		return
	}
	lm.logger.Sugar().Warn(args...)
}

// Error 记录错误日志
func Error(args ...interface{}) {
	if lm == nil {
		return
	}
	lm.logger.Sugar().Error(args...)
}

// Panic 记录恐慌日志
func Panic(args ...interface{}) {
	if lm == nil {
		stdlog.Panic(args...)
		return
	}
	lm.logger.Sugar().Panic(args...)
}

// Fatal 记录致命日志
func Fatal(args ...interface{}) {
	if lm == nil {
		stdlog.Fatal(args...)
		return
	}
	lm.logger.Sugar().Fatal(args...)
}

// Debugf 记录格式化调试日志
func Debugf(template string, args ...interface{}) {
	if lm == nil {
		return
	}
	lm.logger.Sugar().Debugf(template, args...)
}

// Infof 记录格式化信息日志
func Infof(template string, args ...interface{}) {
	if lm == nil {
		return
	}
	lm.logger.Sugar().Infof(template, args...)
}

// Warnf 记录格式化警告日志
func Warnf(template string, args ...interface{}) {
	if lm == nil {
		return
	}
	lm.logger.Sugar().Warnf(template, args...)
}

// Errorf 记录格式化错误日志
func Errorf(template string, args ...interface{}) {
	if lm == nil {
		return
	}
	lm.logger.Sugar().Errorf(template, args...)
}

// Panicf 记录格式化恐慌日志
func Panicf(template string, args ...interface{}) {
	if lm == nil {
		stdlog.Panicf(template, args...)
		return
	}
	lm.logger.Sugar().Panicf(template, args...)
}

// Fatalf 记录格式化致命日志
func Fatalf(template string, args ...interface{}) {
	if lm == nil {
		stdlog.Fatalf(template, args...)
		return
	}
	lm.logger.Sugar().Fatalf(template, args...)
}
